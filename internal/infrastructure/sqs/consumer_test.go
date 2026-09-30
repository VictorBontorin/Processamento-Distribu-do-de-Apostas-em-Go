package sqs

import "testing"

func TestRetryDelaySeconds(t *testing.T) {
	tests := []struct {
		receiveCount int
		want         int32
	}{
		{0, 2},
		{1, 2},
		{2, 4},
		{3, 8},
		{6, 64},
		{7, 64},
		{50, 64},
	}

	for _, tt := range tests {
		if got := retryDelaySeconds(tt.receiveCount); got != tt.want {
			t.Fatalf("receiveCount=%d: got %d, want %d", tt.receiveCount, got, tt.want)
		}
	}
}
