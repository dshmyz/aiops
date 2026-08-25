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
		{name: "合法", request: Request{Domain: "glusterfs", Environment: "prod"}, wantErr: false},
		{name: "环境必填", request: Request{Domain: "glusterfs", Environment: "  "}, wantErr: true},
		{name: "环境超长", request: Request{Domain: "glusterfs", Environment: strings.Repeat("e", 128)}, wantErr: true},
		{name: "资源名超长", request: Request{Domain: "glusterfs", Environment: "prod", ResourceName: strings.Repeat("r", 128)}, wantErr: true},
		{name: "资源名可为空", request: Request{Domain: "glusterfs", Environment: "prod", ResourceName: ""}, wantErr: false},
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