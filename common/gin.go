package common

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/constant"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const KeyRequestBody = "key_request_body"

func GetRequestBody(c *gin.Context) ([]byte, error) {
	requestBody, _ := c.Get(KeyRequestBody)
	if requestBody != nil {
		return requestBody.([]byte), nil
	}
	requestBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	_ = c.Request.Body.Close()
	c.Set(KeyRequestBody, requestBody)
	return requestBody.([]byte), nil
}

// GetResponseBody 从 gin 上下文中获取响应体的字节切片。
// 参数 c 为 gin 上下文，包含请求和响应相关信息。
// 返回值为响应体的字节切片和可能出现的错误。若成功获取响应体则返回字节切片和 nil；若出现错误则返回 nil 和相应错误信息。
func GetResponseBody(c *gin.Context) ([]byte, error) {
	// 从上下文中获取响应对象
	resp, exists := c.Get("response")
	if !exists {
		return nil, errors.New("未能从上下文中获取响应")
	}
	// 将获取到的响应对象转换为 *http.Response 类型
	httpResp, ok := resp.(*http.Response)
	if !ok {
		return nil, errors.New("响应类型转换失败")
	}
	// 读取响应体的所有内容
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %v", err)
	}
	// 成功读取响应体，返回响应体字节切片和 nil
	return responseBody, nil
}

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	requestBody, err := GetRequestBody(c)
	if err != nil {
		return err
	}
	contentType := c.Request.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		err = UnmarshalJson(requestBody, &v)
	} else {
		// skip for now
		// TODO: someday non json request have variant model, we will need to implementation this
	}
	if err != nil {
		return err
	}
	// Reset request body
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return nil
}

func SetContextKey(c *gin.Context, key constant.ContextKey, value any) {
	c.Set(string(key), value)
}

func GetContextKey(c *gin.Context, key constant.ContextKey) (any, bool) {
	return c.Get(string(key))
}

func GetContextKeyString(c *gin.Context, key constant.ContextKey) string {
	return c.GetString(string(key))
}

func GetContextKeyInt(c *gin.Context, key constant.ContextKey) int {
	return c.GetInt(string(key))
}

func GetContextKeyBool(c *gin.Context, key constant.ContextKey) bool {
	return c.GetBool(string(key))
}

func GetContextKeyStringSlice(c *gin.Context, key constant.ContextKey) []string {
	return c.GetStringSlice(string(key))
}

func GetContextKeyStringMap(c *gin.Context, key constant.ContextKey) map[string]any {
	return c.GetStringMap(string(key))
}

func GetContextKeyTime(c *gin.Context, key constant.ContextKey) time.Time {
	return c.GetTime(string(key))
}
