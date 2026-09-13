package main

import (
	"encoding/json"
	"testing"
)

func TestClaudeCancellationRequiresStructuredResultFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "nested cancelled status", data: `{"result":{"status":"cancelled"}}`, want: true},
		{name: "explicit denied flag", data: `{"denied":true}`, want: true},
		{name: "message mentions denied", data: `{"message":"the request was denied by policy"}`, want: false},
		{name: "successful result", data: `{"result":{"status":"completed"}}`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := agentEventInput{RawPayload: json.RawMessage(test.data), Data: json.RawMessage(test.data)}
			if got := lifecyclePayloadIndicatesCancellation(input); got != test.want {
				t.Fatalf("cancellation = %v, want %v", got, test.want)
			}
		})
	}
}
