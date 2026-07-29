module github.com/piotrlaczkowski/slmcode

go 1.23.0

require (
	github.com/piotrlaczkowski/GoLangGraph v0.0.0
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.9.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/sashabaranov/go-openai v1.40.5 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	golang.org/x/sys v0.33.0 // indirect
)

// Local development: GoLangGraph as a sibling under GoLangGraph-Project.
// For publishing, remove this replace and depend on a tagged release.
replace github.com/piotrlaczkowski/GoLangGraph => ../GoLangGraph-Project/GoLangGraph
