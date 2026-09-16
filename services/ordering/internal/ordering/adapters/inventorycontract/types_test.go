package inventorycontract

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func contractFixture(t *testing.T, filename string) *os.File {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate contract test source")
	}
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..", "..", "contracts", "inventory", "v1", filename)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", filename, err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestVersionedExamplesDecodeStrictly(t *testing.T) {
	t.Run("commit requested", func(t *testing.T) {
		var value CommitRequested
		if err := DecodeStrict(contractFixture(t, "inventory-commit-requested.example.json"), &value); err != nil {
			t.Fatalf("DecodeStrict() error = %v", err)
		}
		if value.SchemaVersion != SchemaVersion || len(value.Items) == 0 {
			t.Fatalf("decoded request = %#v", value)
		}
	})

	t.Run("committed", func(t *testing.T) {
		var value Committed
		if err := DecodeStrict(contractFixture(t, "inventory-committed.example.json"), &value); err != nil {
			t.Fatalf("DecodeStrict() error = %v", err)
		}
		if value.SchemaVersion != SchemaVersion || value.Result != "COMMITTED" {
			t.Fatalf("decoded result = %#v", value)
		}
	})

	t.Run("items unavailable", func(t *testing.T) {
		var value ItemsUnavailable
		if err := DecodeStrict(contractFixture(t, "inventory-items-unavailable.example.json"), &value); err != nil {
			t.Fatalf("DecodeStrict() error = %v", err)
		}
		if value.SchemaVersion != SchemaVersion || value.Result != "ITEMS_UNAVAILABLE" || len(value.Items) == 0 {
			t.Fatalf("decoded result = %#v", value)
		}
	})
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	input := `{"schema_version":"1.0","event_id":"a","operation_id":"b","order_id":"c","result":"COMMITTED","occurred_at":"now","processing_state":"PUBLISHED"}`
	var result Committed
	if err := DecodeStrict(strings.NewReader(input), &result); err == nil {
		t.Fatal("DecodeStrict() accepted an unknown processing field")
	}
}

func TestExamplesValidateAgainstJSONSchema(t *testing.T) {
	pairs := []struct {
		schema  string
		example string
	}{
		{"inventory-commit-requested.schema.json", "inventory-commit-requested.example.json"},
		{"inventory-committed.schema.json", "inventory-committed.example.json"},
		{"inventory-items-unavailable.schema.json", "inventory-items-unavailable.example.json"},
	}

	for _, pair := range pairs {
		t.Run(pair.example, func(t *testing.T) {
			compiler := jsonschema.NewCompiler()
			schemaDocument, err := jsonschema.UnmarshalJSON(contractFixture(t, pair.schema))
			if err != nil {
				t.Fatalf("decode schema: %v", err)
			}
			if err := compiler.AddResource(pair.schema, schemaDocument); err != nil {
				t.Fatalf("add schema resource: %v", err)
			}
			schema, err := compiler.Compile(pair.schema)
			if err != nil {
				t.Fatalf("compile schema: %v", err)
			}
			instance, err := jsonschema.UnmarshalJSON(contractFixture(t, pair.example))
			if err != nil {
				t.Fatalf("decode example: %v", err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("example does not match schema: %v", err)
			}
		})
	}
}
