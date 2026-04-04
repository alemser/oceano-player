package main

import "testing"

func TestParseFpcalcOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "standard output",
			input: "DURATION=240\nFINGERPRINT=AQADtJmSSaklHMmSSaRX\n",
			want:  "AQADtJmSSaklHMmSSaRX",
		},
		{
			name:  "fingerprint with trailing whitespace",
			input: "DURATION=120\nFINGERPRINT=  AQABz1GJUEGUAAAB  \n",
			want:  "AQABz1GJUEGUAAAB",
		},
		{
			name:  "windows line endings",
			input: "DURATION=180\r\nFINGERPRINT=AQABz0mUaEkS\r\n",
			want:  "AQABz0mUaEkS",
		},
		{
			name:    "missing fingerprint line",
			input:   "DURATION=240\n",
			wantErr: true,
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "fingerprint value empty",
			input:   "DURATION=240\nFINGERPRINT=\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFpcalcOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("parseFpcalcOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewStartupFingerprinter_UsesFpcalcFromPath(t *testing.T) {
	got, ok := newStartupFingerprinter().(*FpcalcFingerprinter)
	if !ok {
		t.Fatalf("startup fingerprinter type = %T, want *FpcalcFingerprinter", newStartupFingerprinter())
	}
	if got.binaryPath != "fpcalc" {
		t.Fatalf("startup fingerprinter binaryPath = %q, want %q", got.binaryPath, "fpcalc")
	}
}
