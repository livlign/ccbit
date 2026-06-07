package transcript

import "testing"

func TestClassifyCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		// the original keywords keep working
		{"go test ./...", "test"},
		{"npm run build", "build"},
		{"pytest -x tests/", "test"},
		{"cargo build --release", "build"},
		{"cargo test", "test"},
		{"npm test", "test"},
		{"yarn test", "test"},
		{"pnpm run test", "test"},
		{"mvn test", "test"},
		{"gradle test", "test"},
		{"tsc --noEmit", "build"},
		{"msbuild MySolution.sln /t:Build", "build"},
		{"mvn compile", "build"},

		// subcommand tools
		{"dotnet test", "test"},
		{"dotnet build", "build"},
		{"go build ./...", "build"},
		{"go vet ./...", "build"},
		{"cargo clippy", "build"},
		{"cargo check", "build"},
		{"cargo nextest run", "test"},
		{"npm ci", "build"},
		{"npm run lint", "build"},
		{"npm run typecheck", "build"},
		{"pnpm build", "build"},
		{"pnpm lint", "build"},
		{"yarn lint", "build"},
		{"bun test", "test"},
		{"bun run build", "build"},
		{"mvn verify", "test"},
		{"mvn package", "build"},
		{"./mvnw verify", "test"},
		{"./gradlew check", "test"},
		{"./gradlew test", "test"},
		{"gradle build", "build"},
		{"bazel test //...", "test"},
		{"bazel build //...", "build"},
		{"make ci", "test"},
		{"make build", "build"},
		{"make test", "test"},
		{"make check", "test"},
		{"just test", "test"},
		{"just lint", "build"},
		{"swift test", "test"},
		{"deno test", "test"},
		{"deno lint", "build"},
		{"mix test", "test"},
		{"rake test", "test"},
		{"sbt compile", "build"},
		{"xcodebuild test -scheme App", "test"},
		{"npx playwright test", "test"},
		{"biome ci", "build"},
		{"nx test myapp", "test"},
		{"turbo build", "build"},
		{"cmake --build build/", "build"},

		// env-var prefixes, paths, flags before the subcommand
		{"FOO=bar go test ./...", "test"},
		{"CGO_ENABLED=0 go build", "build"},
		{"make -j4 test", "test"},
		{"make VERBOSE=1 test", "test"},
		{"/usr/local/bin/cargo test", "test"},

		// standalone runners
		{"pytest", "test"},
		{"tox", "test"},
		{"nox -s tests", "test"},
		{"npx vitest", "test"},
		{"vitest run", "test"},
		{"npx jest --ci", "test"},
		{"mocha spec/", "test"},
		{"bundle exec rspec", "test"},
		{"phpunit", "test"},
		{"ctest --output-on-failure", "test"},
		{"gotestsum ./...", "test"},
		{"golangci-lint run", "build"},
		{"eslint .", "build"},
		{"ruff check .", "build"},
		{"mypy src/", "build"},
		{"flake8", "build"},
		{"rubocop", "build"},
		{"pyright", "build"},
		{"staticcheck ./...", "build"},
		{"ninja -C out", "build"},

		// compound commands; test outranks build
		{"go build ./... && go test ./...", "test"},
		{"cd app && npm test", "test"},
		{"make build; make test", "test"},

		// exclusions: run/serve/start/dev/watch are never gates
		{"dotnet run", ""},
		{"dotnet run --project src/App", ""},
		{"npm start", ""},
		{"cargo run", ""},
		{"cargo run --bin server", ""},
		{"make serve", ""},
		{"go run main.go", ""},
		{"npm run dev", ""},
		{"npm run watch", ""},
		{"yarn dev", ""},
		{"bun run start", ""},
		{"jest --watch", ""},
		{"vitest --watch", ""},
		{"cargo watch -x test", ""},

		// fail-safe: unrecognized commands never gate
		{"./scripts/ci.sh", ""},
		{"make frobnicate", ""},
		{"make", ""},
		{"cargo", ""},
		{"npm", ""},
		{"ls -la", ""},
		{"echo build complete", ""},
		{"git commit -m 'fix tests'", ""},
		{"grep -rn pattern .", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := classifyCommand(tc.cmd); got != tc.want {
				t.Errorf("classifyCommand(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}
