package diagnostics

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRequestFields(t *testing.T) {
	cases := []struct {
		name    string
		request Request
		wantErr bool
	}{
		{name: "合法", request: Request{Domain: "glusterfs"}, wantErr: false},
		{name: "域名必填", request: Request{Domain: "  "}, wantErr: true},
		{name: "域名超长", request: Request{Domain: strings.Repeat("d", 128)}, wantErr: true},
		{name: "资源名超长", request: Request{Domain: "glusterfs", ResourceName: strings.Repeat("r", 128)}, wantErr: true},
		{name: "资源名可为空", request: Request{Domain: "glusterfs", ResourceName: ""}, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRequestFields(tc.request)
			if tc.wantErr && !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ValidateRequestFields error = %v, want ErrInvalidRequest", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateRequestFields error = %v, want nil", err)
			}
		})
	}
}