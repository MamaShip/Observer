// This file only keeps Go tooling (go build ./..., go test ./..., golangci-lint)
// out of web/: some npm packages ship .go files inside node_modules.
module github.com/MamaShip/Observer/web-ignore

go 1.25
