package quark

import "testing"

func TestUploadURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		bucket    string
		objectKey string
		want      string
		wantErr   bool
	}{
		{
			name:      "http endpoint",
			baseURL:   "http://oss-cn.example.com",
			bucket:    "bucket-a",
			objectKey: "folder/file.txt",
			want:      "https://bucket-a.oss-cn.example.com/folder/file.txt",
		},
		{
			name:      "https endpoint",
			baseURL:   "https://oss-cn.example.com",
			bucket:    "bucket-a",
			objectKey: "/folder/file.txt",
			want:      "https://bucket-a.oss-cn.example.com/folder/file.txt",
		},
		{
			name:      "endpoint without scheme",
			baseURL:   "oss-cn.example.com",
			bucket:    "bucket-a",
			objectKey: "file.txt",
			want:      "https://bucket-a.oss-cn.example.com/file.txt",
		},
		{
			name:      "missing endpoint",
			bucket:    "bucket-a",
			objectKey: "file.txt",
			wantErr:   true,
		},
		{
			name:    "missing target",
			baseURL: "https://oss-cn.example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pre UpPreResp
			pre.Data.UploadUrl = tt.baseURL
			pre.Data.Bucket = tt.bucket
			pre.Data.ObjKey = tt.objectKey
			got, err := uploadURL(pre)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("uploadURL() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("uploadURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("uploadURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
