package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"one-api/common"
	"one-api/common2"
	"one-api/constant"
	constant2 "one-api/constant"
	"one-api/dto"
	"one-api/middleware"
	"one-api/model"
	"one-api/relay"
	relayconstant "one-api/relay/constant"
	"one-api/relay/helper"
	"one-api/service"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// relayHandler 根据中继模式调用不同的助手函数处理请求，并在必要时记录错误日志。
// 参数 c 为 gin 上下文，包含请求和响应相关信息。
// 参数 relayMode 为中继模式，用于确定具体的处理方式。
// 返回值为 OpenAI 错误响应结构体指针，若请求成功则返回 nil。
func relayHandler(c *gin.Context, relayMode int) *dto.OpenAIErrorWithStatusCode {
	var err *dto.OpenAIErrorWithStatusCode

	// 根据不同的中继模式调用对应的助手函数
	switch relayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		// 图像生成或编辑接口，调用 ImageHelper 处理
		err = relay.ImageHelper(c)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		// 语音合成、翻译或转录接口，调用 AudioHelper 处理
		err = relay.AudioHelper(c)
	case relayconstant.RelayModeRerank:
		// 重新排序接口，调用 RerankHelper 处理
		err = relay.RerankHelper(c, relayMode)
	case relayconstant.RelayModeEmbeddings:
		// 嵌入向量接口，调用 EmbeddingHelper 处理
		err = relay.EmbeddingHelper(c)
	case relayconstant.RelayModeResponses:
		// 响应接口，调用 ResponsesHelper 处理
		err = relay.ResponsesHelper(c)
	case relayconstant.RelayModeGemini:
		// Gemini 相关接口，调用 GeminiHelper 处理
		err = relay.GeminiHelper(c)
	default:
		// 其他情况，调用 TextHelper 处理
		err = relay.TextHelper(c)
	}

	//【重要】异步调用SaveReqAndRespToIdrive函数
	go SaveReqAndRespToIdrive(c)

	// 若开启错误日志记录且发生错误，则记录错误日志
	if constant2.ErrorLogEnabled && err != nil {
		// 从上下文中获取用户 ID
		userId := c.GetInt("id")
		// 从上下文中获取令牌名称
		tokenName := c.GetString("token_name")
		// 从上下文中获取原始模型名称
		modelName := c.GetString("original_model")
		// 从上下文中获取令牌 ID
		tokenId := c.GetInt("token_id")
		// 从上下文中获取用户分组信息
		userGroup := c.GetString("group")
		// 从上下文中获取渠道 ID
		channelId := c.GetInt("channel_id")
		// 初始化一个 map 用于存储额外的错误信息
		other := make(map[string]interface{})
		// 记录错误类型
		other["error_type"] = err.Error.Type
		// 记录错误代码
		other["error_code"] = err.Error.Code
		// 记录 HTTP 状态码
		other["status_code"] = err.StatusCode
		// 记录渠道 ID
		other["channel_id"] = channelId
		// 记录渠道名称
		other["channel_name"] = c.GetString("channel_name")
		// 记录渠道类型
		other["channel_type"] = c.GetInt("channel_type")

		// 调用 RecordErrorLog 函数将错误信息记录到 MySQL 中
		model.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.Error.Message, tokenId, 0, false, userGroup, other)
	}

	return err
}

// SaveReqAndRespToIdrive 从上下文中提取 request_id、request 和 response，上传到 Idrive，并将 request_id 存储到 MySQL
// 参数 c 为 gin 上下文，包含请求和响应相关信息
func SaveReqAndRespToIdrive(c *gin.Context) {
	type JsonContent struct {
		RequestId    string `json:"request_id"`
		RequestBody  any    `json:"requestBody"`
		ResponseBody any    `json:"responseBody"`
	}
	var jsonContent JsonContent

	// 从上下文中获取 request_id
	requestId := c.GetString(common.RequestIdKey)
	if requestId == "" {
		common.LogError(c, "未能从上下文中获取 request_id")
		return
	}
	jsonContent.RequestId = requestId

	// 从上下文中获取请求体
	requestBody, err := common.GetRequestBody(c)
	if err != nil {
		common.LogError(c, fmt.Sprintf("获取请求体失败: %v", err))
		return
	}
	// 将请求体反序列化为 map[string]interface{}
	var reqBodyMap any
	err = json.Unmarshal(requestBody, &reqBodyMap)
	if err != nil {
		common.LogError(c, fmt.Sprintf("请求体反序列化失败: %v", err))
		return
	}
	jsonContent.RequestBody = reqBodyMap

	// 从上下文中获取响应
	responseBody, err := common.GetResponseBody(c)
	if err != nil {
		common.LogError(c, fmt.Sprintf("获取响应体失败: %v", err))
		return
	}
	// 将响应体反序列化为 map[string]interface{}
	var respBodyMap any
	err = json.Unmarshal(responseBody, &respBodyMap)
	if err != nil {
		common.LogError(c, fmt.Sprintf("响应体反序列化失败: %v", err))
		return
	}
	jsonContent.ResponseBody = respBodyMap

	jsonContentBytes, err := json.Marshal(jsonContent)
	if err != nil {
		common.LogError(c, fmt.Sprintf("Json 序列化失败: %v", err))
		return
	}
	//上传jsonContentBytes到idrive
	_, err = common2.UploadToIdrive(c, "", requestId, jsonContentBytes)
	if err != nil {
		common.LogError(c, fmt.Sprintf("Json 上传到 Idrive 失败: %v", err))
		return
	}

	// 将 request_id 存储到 MySQL
	err = model.SaveRequestId(c, requestId)
	if err != nil {
		common.LogError(c, fmt.Sprintf("request_id 存储到 MySQL 失败: %v", err))
		return
	}
}

// Relay 处理中继请求，根据请求路径确定中继模式，尝试获取合适的渠道进行请求处理，支持重试机制。
// 若请求失败，会记录错误日志，必要时禁用渠道，并在重试次数耗尽后返回错误响应。
func Relay(c *gin.Context) {
	// 根据请求的 URL 路径确定中继模式
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)

	requestId := c.GetString(common.RequestIdKey)
	group := c.GetString("group")
	// 从上下文中获取原始模型名称
	originalModel := c.GetString("original_model")

	// 定义 OpenAI 错误响应结构体指针
	var openaiErr *dto.OpenAIErrorWithStatusCode

	// 循环重试请求，最多重试 common.RetryTimes 次
	for i := 0; i <= common.RetryTimes; i++ {
		// 获取合适的渠道，传入上下文、用户分组、原始模型名称和重试次数
		channel, err := getChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, err.Error())
			// 将错误包装为 OpenAI 错误响应
			openaiErr = service.OpenAIErrorWrapperLocal(err, "get_channel_failed", http.StatusInternalServerError)
			break
		}

		// 使用获取到的渠道进行中继请求
		openaiErr = relayRequest(c, relayMode, channel)

		if openaiErr == nil {
			// 请求成功，直接返回
			return
		}

		// 异步处理渠道错误，传入上下文、渠道 ID、渠道类型、渠道名称、是否自动封禁和错误信息
		go processChannelError(c, channel.Id, channel.Type, channel.Name, channel.GetAutoBan(), openaiErr)

		// 根据错误信息判断是否需要重试
		if !shouldRetry(c, openaiErr, common.RetryTimes-i) {
			// 不需要重试，跳出循环
			break
		}
	}

	// 从上下文中获取使用过的渠道列表
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		// 若使用过多个渠道，记录重试日志
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}

	if openaiErr != nil {
		if openaiErr.StatusCode == http.StatusTooManyRequests {
			// 记录原始 429 错误日志
			common.LogError(c, fmt.Sprintf("origin 429 error: %s", openaiErr.Error.Message))
			// 修改错误信息提示用户上游负载饱和
			openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		// 将请求 ID 添加到错误信息中
		openaiErr.Error.Message = common.MessageWithRequestId(openaiErr.Error.Message, requestId)
		// 返回错误响应给客户端
		c.JSON(openaiErr.StatusCode, gin.H{
			"error": openaiErr.Error,
		})
	}
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func WssRelay(c *gin.Context) {
	// 将 HTTP 连接升级为 WebSocket 连接

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	defer ws.Close()

	if err != nil {
		openaiErr := service.OpenAIErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError)
		helper.WssError(c, ws, openaiErr.Error)
		return
	}

	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)
	requestId := c.GetString(common.RequestIdKey)
	group := c.GetString("group")
	//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
	originalModel := c.GetString("original_model")
	var openaiErr *dto.OpenAIErrorWithStatusCode

	for i := 0; i <= common.RetryTimes; i++ {
		channel, err := getChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, err.Error())
			openaiErr = service.OpenAIErrorWrapperLocal(err, "get_channel_failed", http.StatusInternalServerError)
			break
		}

		openaiErr = wssRequest(c, ws, relayMode, channel)

		if openaiErr == nil {
			return // 成功处理请求，直接返回
		}

		go processChannelError(c, channel.Id, channel.Type, channel.Name, channel.GetAutoBan(), openaiErr)

		if !shouldRetry(c, openaiErr, common.RetryTimes-i) {
			break
		}
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}

	if openaiErr != nil {
		if openaiErr.StatusCode == http.StatusTooManyRequests {
			openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		openaiErr.Error.Message = common.MessageWithRequestId(openaiErr.Error.Message, requestId)
		helper.WssError(c, ws, openaiErr.Error)
	}
}

func RelayClaude(c *gin.Context) {
	//relayMode := constant.Path2RelayMode(c.Request.URL.Path)
	requestId := c.GetString(common.RequestIdKey)
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	var claudeErr *dto.ClaudeErrorWithStatusCode

	for i := 0; i <= common.RetryTimes; i++ {
		channel, err := getChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, err.Error())
			claudeErr = service.ClaudeErrorWrapperLocal(err, "get_channel_failed", http.StatusInternalServerError)
			break
		}

		claudeErr = claudeRequest(c, channel)

		if claudeErr == nil {
			return // 成功处理请求，直接返回
		}

		openaiErr := service.ClaudeErrorToOpenAIError(claudeErr)

		go processChannelError(c, channel.Id, channel.Type, channel.Name, channel.GetAutoBan(), openaiErr)

		if !shouldRetry(c, openaiErr, common.RetryTimes-i) {
			break
		}
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}

	if claudeErr != nil {
		claudeErr.Error.Message = common.MessageWithRequestId(claudeErr.Error.Message, requestId)
		c.JSON(claudeErr.StatusCode, gin.H{
			"type":  "error",
			"error": claudeErr.Error,
		})
	}
}

// relayRequest 处理中继请求，将请求转发到指定渠道，并返回可能的 OpenAI 错误响应。
// 参数 c 为 gin 上下文，包含请求和响应相关信息。
// 参数 relayMode 为中继模式，用于确定请求的处理方式。
// 参数 channel 为要使用的渠道信息。
// 返回值为 OpenAI 错误响应结构体指针，若请求成功则返回 nil。
func relayRequest(c *gin.Context, relayMode int, channel *model.Channel) *dto.OpenAIErrorWithStatusCode {
	// 将当前使用的渠道 ID 添加到上下文中的使用渠道列表中
	addUsedChannel(c, channel.Id)

	// 从上下文中获取请求体内容
	requestBody, _ := common.GetRequestBody(c)

	// 重新设置请求体，以便后续处理可以重复读取请求体内容
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

	// 调用 relayHandler 函数处理中继请求，并返回可能的错误响应
	return relayHandler(c, relayMode)
}

func wssRequest(c *gin.Context, ws *websocket.Conn, relayMode int, channel *model.Channel) *dto.OpenAIErrorWithStatusCode {
	addUsedChannel(c, channel.Id)
	requestBody, _ := common.GetRequestBody(c)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return relay.WssHelper(c, ws)
}

func claudeRequest(c *gin.Context, channel *model.Channel) *dto.ClaudeErrorWithStatusCode {
	addUsedChannel(c, channel.Id)
	requestBody, _ := common.GetRequestBody(c)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return relay.ClaudeHelper(c)
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

// getChannel 根据重试次数获取合适的渠道。
// 若重试次数为 0，直接从上下文中获取渠道信息；否则，从缓存中获取随机满足条件的渠道。
// 参数 c 为 gin 上下文，group 为用户分组，originalModel 为原始模型名称，retryCount 为重试次数。
// 返回值为渠道指针和错误信息。
func getChannel(c *gin.Context, group, originalModel string, retryCount int) (*model.Channel, error) {
	// 若重试次数为 0，直接从上下文中获取渠道信息
	if retryCount == 0 {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		return &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}, nil
	}
	// 若重试次数不为 0，从缓存中获取随机满足条件的渠道
	channel, _, err := model.CacheGetRandomSatisfiedChannel(c, group, originalModel, retryCount)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("获取重试渠道失败: %s", err.Error()))
	}
	// 为选中的渠道设置上下文信息
	middleware.SetupContextForSelectedChannel(c, channel, originalModel)
	return channel, nil
}

func shouldRetry(c *gin.Context, openaiErr *dto.OpenAIErrorWithStatusCode, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if openaiErr.LocalError {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if openaiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if openaiErr.StatusCode == 307 {
		return true
	}
	if openaiErr.StatusCode/100 == 5 {
		// 超时不重试
		if openaiErr.StatusCode == 504 || openaiErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if openaiErr.StatusCode == http.StatusBadRequest {
		channelType := c.GetInt("channel_type")
		if channelType == constant.ChannelTypeAnthropic {
			return true
		}
		return false
	}
	if openaiErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if openaiErr.StatusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelError(c *gin.Context, channelId int, channelType int, channelName string, autoBan bool, err *dto.OpenAIErrorWithStatusCode) {
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	common.LogError(c, fmt.Sprintf("relay error (channel #%d, status code: %d): %s", channelId, err.StatusCode, err.Error.Message))
	if service.ShouldDisableChannel(channelType, err) && autoBan {
		service.DisableChannel(channelId, channelName, err.Error.Message)
	}
}

func RelayMidjourney(c *gin.Context) {
	relayMode := c.GetInt("relay_mode")
	var err *dto.MidjourneyResponse
	switch relayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		err = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		err = relay.RelayMidjourneyTask(c, relayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		err = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		err = relay.RelaySwapFace(c)
	default:
		err = relay.RelayMidjourneySubmit(c, relayMode)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(err)
	if err != nil {
		statusCode := http.StatusBadRequest
		if err.Code == 30 {
			err.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", err.Description, err.Result),
			"type":        "upstream_error",
			"code":        err.Code,
		})
		channelId := c.GetInt("channel_id")
		common.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", err.Description, err.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := dto.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := dto.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayTask(c *gin.Context) {
	retryTimes := common.RetryTimes
	channelId := c.GetInt("channel_id")
	relayMode := c.GetInt("relay_mode")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	c.Set("use_channel", []string{fmt.Sprintf("%d", channelId)})
	taskErr := taskRelayHandler(c, relayMode)
	if taskErr == nil {
		retryTimes = 0
	}
	for i := 0; shouldRetryTaskRelay(c, channelId, taskErr, retryTimes) && i < retryTimes; i++ {
		channel, _, err := model.CacheGetRandomSatisfiedChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, fmt.Sprintf("CacheGetRandomSatisfiedChannel failed: %s", err.Error()))
			break
		}
		channelId = channel.Id
		useChannel := c.GetStringSlice("use_channel")
		useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
		c.Set("use_channel", useChannel)
		common.LogInfo(c, fmt.Sprintf("using channel #%d to retry (remain times %d)", channel.Id, i))
		middleware.SetupContextForSelectedChannel(c, channel, originalModel)

		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		taskErr = taskRelayHandler(c, relayMode)
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}
	if taskErr != nil {
		if taskErr.StatusCode == http.StatusTooManyRequests {
			taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		c.JSON(taskErr.StatusCode, taskErr)
	}
}

func taskRelayHandler(c *gin.Context, relayMode int) *dto.TaskError {
	var err *dto.TaskError
	switch relayMode {
	case relayconstant.RelayModeSunoFetch, relayconstant.RelayModeSunoFetchByID, relayconstant.RelayModeKlingFetchByID:
		err = relay.RelayTaskFetch(c, relayMode)
	default:
		err = relay.RelayTaskSubmit(c, relayMode)
	}
	return err
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *dto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if taskErr.StatusCode == 504 || taskErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
