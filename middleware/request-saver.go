package middleware

import (
	"bytes"
	"context"
	"io"
	"one-api/common"
	"one-api/common2"
	"one-api/model"
	"time"

	"github.com/gin-gonic/gin"
)

// AsyncRequestSaver 异步请求保存中间件
// 该中间件在 POST 请求到达时触发，读取请求体并将其异步保存到 iDrive 和数据库。
// 若保存过程中出现错误，将记录错误日志。
// 该中间件不阻塞请求处理，确保请求能够快速响应。
func AsyncRequestSaver() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 只在POST方法时保存请求
		if c.Request.Method == "POST" {
			// 读取请求体
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 恢复body

			// 在goroutine中异步处理
			go func(body []byte, path string) {
				// 获取请求ID
				requestId := c.GetString(common.RequestIdKey)

				// 1. 保存到Idrive
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				if _, err := common2.UploadToIdrive(ctx, "", requestId, body); err != nil {
					common.LogError(c, common.MessageWithRequestId("Idrive上传失败", requestId)+": "+err.Error())

				}

				// 2. 保存日志到数据库
				if err := model.SaveRequestId(c, requestId); err != nil {
					common.LogError(c, common.MessageWithRequestId("日志保存失败", requestId)+": "+err.Error())
				}
			}(bodyBytes, c.Request.URL.Path)
		}

		c.Next()
	}
}
