package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"one-api/common"
	"one-api/common2"
	"one-api/model"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func TestSaveReqAndRespToIdrive(t *testing.T) {
	var err error

	// 获取当前文件所在目录
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}

	// 拼接 .env 文件路径，假设 .env 文件在项目根目录
	envPath := filepath.Join(dir, "../.env") // 根据实际情况调整路径

	// 尝试加载 .env 文件中的环境变量
	err = godotenv.Load(envPath)
	if err != nil {
		// 若未找到 .env 文件，输出提示信息，使用系统默认环境变量
		common.SysLog("未找到 .env 文件，使用默认环境变量，如果需要，请创建 .env 文件并设置相关变量")
		return
	}

	// 加载环境变量，将环境变量的值初始化到 common 包的相关变量中
	common.InitEnv()

	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to init default db client: " + err.Error())
		return
	}

	//初始化Log数据库
	err = model.InitLogDB()
	if err != nil {
		common.FatalLog("failed to init log db client: " + err.Error())
		return
	}

	//初始化idrive
	err = common2.InitIdriveClient()
	if err != nil {
		common.FatalLog("failed to init idrive client: " + err.Error())
		return
	}

	//初始化上下文，并且构造request，以及set response
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
