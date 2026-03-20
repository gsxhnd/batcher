package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMakeDir(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		setup   func() // 可选的设置函数
		cleanup func() // 清理函数
	}{
		{
			name:    "create_new_dir",
			path:    "../testdata/test_new_dir",
			wantErr: false,
			cleanup: func() { os.RemoveAll("../testdata/test_new_dir") },
		},
		{
			name:    "existing_dir",
			path:    "../testdata",
			wantErr: false,
		},
		{
			name:    "empty_path",
			path:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			if tt.cleanup != nil {
				defer tt.cleanup()
			}

			err := MakeDir(tt.path)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
