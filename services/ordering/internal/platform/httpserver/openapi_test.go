package httpserver

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIContractIsValid(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate OpenAPI test source")
	}
	contractPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..", "contracts", "http", "openapi.json")

	document, err := openapi3.NewLoader().LoadFromFile(contractPath)
	if err != nil {
		t.Fatalf("load OpenAPI contract: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI contract: %v", err)
	}
}
