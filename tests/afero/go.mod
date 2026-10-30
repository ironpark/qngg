module github.com/ironpark/qngg/tests/afero

go 1.27

require (
	github.com/ironpark/qngg v0.0.0
	github.com/spf13/afero v1.15.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/ebitengine/purego v0.11.2 // indirect
	github.com/ironpark/wasmtime-pure-go v0.0.0-20261027140914-a51037ed9202 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/text v0.28.0 // indirect
)

replace github.com/ironpark/qngg => ../..
