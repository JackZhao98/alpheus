module alpheus/kernel

go 1.22

require (
	github.com/lib/pq v1.10.9
	github.com/robfig/cron/v3 v3.0.1
	gopkg.in/yaml.v3 v3.0.1
)

// sandbox-only mirror; harmless to keep — same module, fetched from GitHub
replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1
