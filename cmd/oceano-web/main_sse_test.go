package main

import "testing"

func TestFormatSSEDataFrame(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "pretty json with trailing newline",
			in:   "{\n  \"state\": \"playing\"\n}\n",
			want: "data: {\n" +
				"data:   \"state\": \"playing\"\n" +
				"data: }\n\n",
		},
		{
			name: "single line json",
			in:   "{\"state\":\"playing\"}",
			want: "data: {\"state\":\"playing\"}\n\n",
		},
		{
			name: "preserves intentional blank lines",
			in:   "line1\n\nline3\n",
			want: "data: line1\n" +
				"data: \n" +
				"data: line3\n\n",
		},
		{
			name: "empty payload still valid",
			in:   "",
			want: "data: \n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSSEDataFrame([]byte(tt.in))
			if got != tt.want {
				t.Fatalf("formatSSEDataFrame() mismatch\nwant:\n%q\ngot:\n%q", tt.want, got)
			}
		})
	}
}
