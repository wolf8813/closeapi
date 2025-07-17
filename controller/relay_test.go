package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"one-api/common"
	"one-api/common2"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSaveReqAndRespToIdrive(t *testing.T) {
	//初始化idrive
	common2.InitIdriveClient()

	//初始化上下文，并且构造request，以及set response
	//c := &gin.Context{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	//初始化请求体
	// 定义要写入的 JSON 内容
	requestBodyJSON := `{
		"model": "gpt-4",
		"messages": [
			{
				"role": "developer",
				"content": "你是一个有帮助的助手。"
			},
			{
				"role": "user",
				"content": "你好！"
			}
		]
	}`
	// 将 JSON 内容写入请求体
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(requestBodyJSON))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	// 模拟响应内容
	respBody := `{
	"id": "chatcmpl-B9MBs8CjcvOU2jLn4n570S5qMJKcT",
	"object": "chat.completion",
	"created": 1741569952,
	"model": "gpt-4.1-2025-04-14",
	"choices": [
		{
		"index": 0,
		"message": {
			"role": "assistant",
			"content": "你好！我能为你提供什么帮助？",
			"refusal": null,
			"annotations": []
		},
		"logprobs": null,
		"finish_reason": "stop"
		}
	],
	"usage": {
		"prompt_tokens": 19,
		"completion_tokens": 10,
		"total_tokens": 29,
		"prompt_tokens_details": {
		"cached_tokens": 0,
		"audio_tokens": 0
		},
		"completion_tokens_details": {
		"reasoning_tokens": 0,
		"audio_tokens": 0,
		"accepted_prediction_tokens": 0,
		"rejected_prediction_tokens": 0
		}
	},
	"service_tier": "default"
	}`
	// 创建一个 bytes.Reader 来包装响应内容
	bodyReader := bytes.NewReader([]byte(respBody))

	// 初始化 httpResp
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:          io.NopCloser(bodyReader),
		ContentLength: int64(len(respBody)),
	}
	//context保存response
	c.Set("response", resp)

	c.Set(common.RequestIdKey, "test_request_id")

	//调用保存函数
	SaveReqAndRespToIdrive(c)

}
