package application

import (
	"errors"
	"testing"
)

type testCodedError string

func (failure testCodedError) Error() string { return string(failure) }
func (failure testCodedError) Code() string  { return string(failure) }

func TestSafeInventoryErrorCodeAllowsOnlyKnownCodes(t *testing.T) {
	for _, code := range []string{"INVENTORY_UNREACHABLE", "INVENTORY_TIMEOUT", "INVENTORY_THROTTLED",
		"INVENTORY_UNAUTHORIZED", "INVENTORY_SERVER_ERROR", "INVENTORY_CONTRACT_REJECTED",
		"INVENTORY_OPERATION_CONFLICT", "INVENTORY_INVALID_RESPONSE"} {
		if got := safeInventoryErrorCode(testCodedError(code)); got != code {
			t.Fatalf("safeInventoryErrorCode(%q) = %q", code, got)
		}
	}
	if got := safeInventoryErrorCode(testCodedError("RAW_DATABASE_ERROR")); got != defaultInventoryErrorCode {
		t.Fatalf("unknown code = %q", got)
	}
	if got := safeInventoryErrorCode(errors.New("raw error")); got != defaultInventoryErrorCode {
		t.Fatalf("plain error code = %q", got)
	}
}
