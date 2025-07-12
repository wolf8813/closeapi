package common2

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInitIdriveClient(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		// TODO: Add test cases.
		{name: "test01", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := InitIdriveClient(); (err != nil) != tt.wantErr {
				t.Errorf("InitIdriveClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUploadToIdrive(t *testing.T) {
	//初始化idrive
	if MinioClient == nil {
		err := InitIdriveClient()
		if err != nil {
			t.Errorf("InitIdriveClient() error = %v", err)
			return
		}
	}

	//上传gpt4.1的request.body
	reqBody := struct {
		Model   string `json:"model"`
		Message []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model: "gpt-4.1",
		Message: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{
				Role:    "developer",
				Content: "你是一个有帮助的助手。",
			},
			{
				Role:    "user",
				Content: "今天周几？",
			},
		},
	}
	reqBodyStr, err := json.Marshal(reqBody)
	if err != nil {
		t.Errorf("json.Marshal() error = %v", err)
		return
	}
	ctx := context.Background()
	objectKey := "123456"
	content := []byte(reqBodyStr)
	_, err = UploadToIdrive(ctx, defaultBucketName, objectKey, content)
	if err != nil {
		t.Errorf("UploadToIdrive() error = %v", err)
		return
	}
	t.Logf("UploadToIdrive() success, objectKey = %v", objectKey)

}

func TestDownloadFromIdrive(t *testing.T) {
	//初始化idrive
	if MinioClient == nil {
		err := InitIdriveClient()
		if err != nil {
			t.Errorf("InitIdriveClient() error = %v", err)
			return
		}
	}

	//下载
	content, err := DownloadFromIdrive(context.Background(), defaultBucketName, "123456")
	if err != nil {
		t.Errorf("DownloadFromIdrive() error = %v", err)
		return
	}
	t.Logf("DownloadFromIdrive() success, content = %v", content)

}
