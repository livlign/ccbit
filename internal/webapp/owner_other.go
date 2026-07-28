//go:build !windows && !linux && !darwin

package webapp

// On a platform with no socket-table lookup implemented, ownership is simply
// unknown: every port reads as "cannot tell", and the caller falls back to
// showing what it verified as listening.
func ownerText(int) (string, bool) { return "", false }
