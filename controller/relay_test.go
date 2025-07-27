package controller

import (
	"bytes"
	"encoding/json"
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

func initTestResource() error {
	// 获取当前文件所在目录
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// 拼接 .env 文件路径，假设 .env 文件在项目根目录
	envPath := filepath.Join(dir, "../.env") // 根据实际情况调整路径

	// 尝试加载 .env 文件中的环境变量
	err = godotenv.Load(envPath)
	if err != nil {
		// 若未找到 .env 文件，输出提示信息，使用系统默认环境变量
		common.SysLog("未找到 .env 文件，使用默认环境变量，如果需要，请创建 .env 文件并设置相关变量")
		return err
	}

	// 加载环境变量，将环境变量的值初始化到 common 包的相关变量中
	common.InitEnv()

	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to init default db client: " + err.Error())
		return err
	}

	//初始化Log数据库
	err = model.InitLogDB()
	if err != nil {
		common.FatalLog("failed to init log db client: " + err.Error())
		return err
	}

	//初始化idrive
	err = common2.InitIdriveClient()
	if err != nil {
		common.FatalLog("failed to init idrive client: " + err.Error())
		return err
	}

	return nil
}

func TestSaveReqAndRespToIdrive(t *testing.T) {
	var err error

	// 初始化测试资源
	err = initTestResource()
	if err != nil {
		t.Fatalf("Failed to initialize test resources: %v", err)
		return
	}

	//初始化上下文，并且构造request，以及set response
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	//初始化请求体
	// 定义要写入的 JSON 内容
	type ReqBody struct {
		Model   string    `json:"model"`
		Message []Message `json:"messages"`
	}
	reqBody := ReqBody{
		Model: "gpt-4",
		Message: []Message{
			{
				Role:    "developer",
				Content: "你是一个有帮助的助手。",
			},
			{
				Role:    "user",
				Content: "你好！",
			},
		},
	}
	byteReqBody, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request body: %v", err)
	}
	// 将 JSON 内容写入请求体
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(byteReqBody))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	type Choice struct {
		Index   int     `json:"index"`
		Message Message `json:"message"`
	}
	type Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	type RespBody struct {
		Id          string   `json:"id"`
		Object      string   `json:"object"`
		Created     int64    `json:"created"`
		Model       string   `json:"model"`
		Choices     []Choice `json:"choices"`
		Usage       Usage    `json:"usage"`
		ServiceTier string   `json:"service_tier"`
	}
	respBody := RespBody{
		Id:          "chatcmpl-B9MBs8CjcvOU2jLn4n570S5qMJKcT",
		Object:      "chat.completion",
		Created:     1741569952,
		Model:       "gpt-4.1-2025-04-14",
		Choices:     []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "你好！我能为你提供什么帮助？"}}},
		Usage:       Usage{PromptTokens: 19, CompletionTokens: 10, TotalTokens: 29},
		ServiceTier: "default",
	}
	byteRespBody, err := json.Marshal(respBody)
	if err != nil {
		t.Fatalf("Failed to marshal response body: %v", err)
	}
	strRespBody := string(byteRespBody)

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
		Body:          io.NopCloser(bytes.NewReader(byteRespBody)),
		ContentLength: int64(len(strRespBody)),
	}
	//context保存response
	c.Set("response", resp)

	c.Set(common.RequestIdKey, "test_request_id")

	//调用保存函数
	SaveReqAndRespToIdrive(c)

}
