package domain

import (
	"errors"
	"testing"
)

func TestCustomerStateHidesTechnicalOrderStates(t *testing.T) {
	tests := []struct {
		name  string
		order Order
		want  CustomerState
	}{
		{name: "submitted", order: Order{Status: OrderSubmitted}, want: CustomerPreparing},
		{name: "confirmed preparing", order: Order{Status: OrderConfirmed, Fulfillment: &Fulfillment{Status: FulfillmentPreparing}}, want: CustomerPreparing},
		{name: "delivering", order: Order{Status: OrderConfirmed, Fulfillment: &Fulfillment{Status: FulfillmentDelivering}}, want: CustomerOnTheWay},
		{name: "delivered", order: Order{Status: OrderConfirmed, Fulfillment: &Fulfillment{Status: FulfillmentDelivered}}, want: CustomerDelivered},
		{name: "rejected", order: Order{Status: OrderRejected}, want: CustomerRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.order.CustomerState()
			if err != nil {
				t.Fatalf("CustomerState() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("CustomerState() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateFulfillmentTarget(t *testing.T) {
	tests := []struct {
		name    string
		current FulfillmentStatus
		target  FulfillmentStatus
		wantErr bool
	}{
		{name: "prepare to delivering", current: FulfillmentPreparing, target: FulfillmentDelivering},
		{name: "deliver to delivered", current: FulfillmentDelivering, target: FulfillmentDelivered},
		{name: "repeat delivering", current: FulfillmentDelivering, target: FulfillmentDelivering},
		{name: "repeat delivered", current: FulfillmentDelivered, target: FulfillmentDelivered},
		{name: "skip delivered", current: FulfillmentPreparing, target: FulfillmentDelivered, wantErr: true},
		{name: "reverse preparing", current: FulfillmentDelivering, target: FulfillmentPreparing, wantErr: true},
		{name: "reverse delivering", current: FulfillmentDelivered, target: FulfillmentDelivering, wantErr: true},
		{name: "repeat preparing is not an action", current: FulfillmentPreparing, target: FulfillmentPreparing, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateFulfillmentTarget(test.current, test.target)
			if test.wantErr && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("error = %v, want ErrInvalidTransition", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
