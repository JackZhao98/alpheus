module alpheus/kernel

go 1.23.0

toolchain go1.23.2

require (
	github.com/lib/pq v1.10.9
	github.com/modelcontextprotocol/go-sdk v1.3.1
	github.com/robfig/cron/v3 v3.0.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.3 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
)

// sandbox-only mirror; harmless to keep — same module, fetched from GitHub
replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1
