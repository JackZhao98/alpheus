module alpheus/agentruntime

go 1.22

require gopkg.in/yaml.v3 v3.0.1

// sandbox-only mirror; harmless to keep — same module, fetched from GitHub
replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1
