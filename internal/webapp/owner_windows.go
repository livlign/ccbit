//go:build windows

package webapp

// Windows socket-to-process lookup, done with direct syscalls rather than a
// shell-out. The obvious route (Get-NetTCPConnection piped into
// Get-CimInstance) measures ~1.5s per port, which a status line that repaints
// every second cannot afford even occasionally. iphlpapi answers the same
// question in about a millisecond.
//
// Everything here degrades to (\"\", false), meaning \"could not tell\", on any
// error: a missing DLL, a process owned by another user, a layout that does not
// look right. Nothing is ever hidden because a lookup failed.

import (
	"encoding/binary"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	modiphlpapi             = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")

	modkernel32           = syscall.NewLazyDLL("kernel32.dll")
	procReadProcessMemory = modkernel32.NewProc("ReadProcessMemory")

	modntdll                      = syscall.NewLazyDLL("ntdll.dll")
	procNtQueryInformationProcess = modntdll.NewProc("NtQueryInformationProcess")
)

const (
	afInet  = 2
	afInet6 = 23
	// TCP_TABLE_OWNER_PID_LISTENER: listening sockets with their owning PID.
	tcpTableOwnerPIDListener = 3

	processQueryLimitedInformation = 0x1000
	processVMRead                  = 0x0010

	// x64 offsets into the process's own structures. Stable since Windows 7 and
	// identical on amd64 and arm64, the only architectures ccbit ships for.
	pebProcessParameters   = 0x20
	paramsCurrentDirectory = 0x38 // CURDIR.DosPath (UNICODE_STRING)
	paramsCommandLine      = 0x70 // UNICODE_STRING

	// maxStringBytes caps a single remote string read. A command line is capped
	// at 32 KiB by the OS; this refuses anything that claims to be larger, which
	// is the signature of a bad read rather than a long command.
	maxStringBytes = 64 << 10
)

// ownerText returns the command line and working directory of whatever is
// listening on port, and whether the lookup itself worked.
func ownerText(port int) (text string, ok bool) {
	// Reading another process's memory is inherently a "layout is what I think
	// it is" bet. It is bounded and checked at every step, but a status line
	// must not die on a surprise.
	defer func() {
		if recover() != nil {
			text, ok = "", false
		}
	}()

	pid, found := listenerPID(port)
	if !found {
		return "", false
	}
	cmd, cwd, err := processStrings(pid)
	if err != nil {
		return "", false
	}
	return cmd + "\x00" + cwd, true
}

// listenerPID finds the process listening on port, checking IPv4 then IPv6.
func listenerPID(port int) (uint32, bool) {
	if pid, ok := scanTCPTable(afInet, port); ok {
		return pid, true
	}
	return scanTCPTable(afInet6, port)
}

// scanTCPTable walks the OS listener table for one address family. The row
// layouts are fixed: 24 bytes for IPv4 (state, local addr/port, remote
// addr/port, pid) and 56 for IPv6 (16-byte addresses plus scope ids). Ports are
// stored network-order in the low half of a DWORD, hence the big-endian read of
// the first two bytes.
func scanTCPTable(family uint32, port int) (uint32, bool) {
	var size uint32
	_, _, _ = procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0,
		uintptr(family), tcpTableOwnerPIDListener, 0)
	if size == 0 || size > 8<<20 {
		return 0, false
	}
	buf := make([]byte, size)
	ret, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if ret != 0 || size < 4 {
		return 0, false
	}

	rowSize, portOff, pidOff := 24, 8, 20
	if family == afInet6 {
		rowSize, portOff, pidOff = 56, 20, 52
	}
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	for i := 0; i < n; i++ {
		start := 4 + i*rowSize
		if start+rowSize > len(buf) {
			break
		}
		row := buf[start : start+rowSize]
		if int(binary.BigEndian.Uint16(row[portOff:portOff+2])) != port {
			continue
		}
		return binary.LittleEndian.Uint32(row[pidOff : pidOff+4]), true
	}
	return 0, false
}

// processBasicInformation is the x64 layout of PROCESS_BASIC_INFORMATION; the
// explicit padding fields keep Go's alignment matching the C struct.
type processBasicInformation struct {
	ExitStatus                   uint32
	_                            uint32
	PebBaseAddress               uintptr
	AffinityMask                 uintptr
	BasePriority                 int32
	_                            int32
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr
}

// processStrings reads a process's command line and current directory out of
// its PEB. Neither is available any other way on Windows without a WMI query.
func processStrings(pid uint32) (cmd, cwd string, err error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation|processVMRead, false, pid)
	if err != nil {
		return "", "", err
	}
	defer syscall.CloseHandle(h)

	var pbi processBasicInformation
	var retLen uint32
	status, _, _ := procNtQueryInformationProcess.Call(uintptr(h), 0,
		uintptr(unsafe.Pointer(&pbi)), unsafe.Sizeof(pbi), uintptr(unsafe.Pointer(&retLen)))
	if status != 0 || pbi.PebBaseAddress == 0 {
		return "", "", syscall.EINVAL
	}

	params, err := readPointer(h, pbi.PebBaseAddress+pebProcessParameters)
	if err != nil || params == 0 {
		return "", "", syscall.EINVAL
	}
	cmd, _ = readUnicodeString(h, params+paramsCommandLine)
	cwd, _ = readUnicodeString(h, params+paramsCurrentDirectory)
	if cmd == "" && cwd == "" {
		return "", "", syscall.EINVAL
	}
	return cmd, cwd, nil
}

func readMemory(h syscall.Handle, addr uintptr, n int) ([]byte, error) {
	buf := make([]byte, n)
	var read uintptr
	ok, _, err := procReadProcessMemory.Call(uintptr(h), addr,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(n), uintptr(unsafe.Pointer(&read)))
	if ok == 0 || int(read) != n {
		if err == nil {
			err = syscall.EINVAL
		}
		return nil, err
	}
	return buf, nil
}

func readPointer(h syscall.Handle, addr uintptr) (uintptr, error) {
	b, err := readMemory(h, addr, 8)
	if err != nil {
		return 0, err
	}
	return uintptr(binary.LittleEndian.Uint64(b)), nil
}

// readUnicodeString reads a UNICODE_STRING (length, capacity, pointer) and then
// the UTF-16 buffer it points at.
func readUnicodeString(h syscall.Handle, addr uintptr) (string, error) {
	head, err := readMemory(h, addr, 16)
	if err != nil {
		return "", err
	}
	length := int(binary.LittleEndian.Uint16(head[0:2]))
	buffer := uintptr(binary.LittleEndian.Uint64(head[8:16]))
	if length == 0 || length > maxStringBytes || buffer == 0 {
		return "", syscall.EINVAL
	}
	if length%2 == 1 {
		length-- // UTF-16: an odd byte count is a bad read, not a half character
	}
	raw, err := readMemory(h, buffer, length)
	if err != nil {
		return "", err
	}
	units := make([]uint16, length/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), nil
}
