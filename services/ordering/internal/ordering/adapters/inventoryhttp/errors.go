package inventoryhttp

import "fmt"

const (
	CodeUnreachable       = "INVENTORY_UNREACHABLE"
	CodeTimeout           = "INVENTORY_TIMEOUT"
	CodeThrottled         = "INVENTORY_THROTTLED"
	CodeUnauthorized      = "INVENTORY_UNAUTHORIZED"
	CodeServerError       = "INVENTORY_SERVER_ERROR"
	CodeContractRejected  = "INVENTORY_CONTRACT_REJECTED"
	CodeOperationConflict = "INVENTORY_OPERATION_CONFLICT"
	CodeInvalidResponse   = "INVENTORY_INVALID_RESPONSE"
)

type Error struct {
	code  string
	cause error
}

func (failure *Error) Error() string { return fmt.Sprintf("%s: %v", failure.code, failure.cause) }
func (failure *Error) Unwrap() error { return failure.cause }
func (failure *Error) Code() string  { return failure.code }

func classified(code string, cause error) error { return &Error{code: code, cause: cause} }
