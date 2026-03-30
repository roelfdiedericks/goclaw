package telegram

import "testing"

func TestClassifyTelegramPollingError(t *testing.T) {
	tests := []struct {
		name      string
		desc      string
		wantFatal bool
	}{
		{
			name:      "other poller conflict",
			desc:      "Conflict: terminated by other getUpdates request; make sure that only one bot instance is running",
			wantFatal: true,
		},
		{
			name:      "webhook active",
			desc:      "Conflict: can't use getUpdates method while webhook is active",
			wantFatal: true,
		},
		{
			name:      "temporary upstream error",
			desc:      "Bad Gateway",
			wantFatal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFatal, _ := classifyTelegramPollingError(&telegramAPIError{
				Method:      "getUpdates",
				StatusCode:  409,
				Description: tt.desc,
			})
			if gotFatal != tt.wantFatal {
				t.Fatalf("got fatal=%v want %v", gotFatal, tt.wantFatal)
			}
		})
	}
}
