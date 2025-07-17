package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/relay/helper"
	"one-api/service"
	"one-api/setting"
	"one-api/setting/model_setting"
	"strings"

	"github.com/gin-gonic/gin"
)

func getAndValidateResponsesRequest(c *gin.Context) (*dto.OpenAIResponsesRequest, error) {
	request := &dto.OpenAIResponsesRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(request.Input) == 0 {
		return nil, errors.New("input is required")
	}
	return request, nil

}

func checkInputSensitive(textRequest *dto.OpenAIResponsesRequest, info *relaycommon.RelayInfo) ([]string, error) {
	sensitiveWords, err := service.CheckSensitiveInput(textRequest.Input)
	return sensitiveWords, err
}

func getInputTokens(req *dto.OpenAIResponsesRequest, info *relaycommon.RelayInfo) int {
	inputTokens := service.CountTokenInput(req.Input, req.Model)
	info.PromptTokens = inputTokens
	return inputTokens
}

// ResponsesHelper 处理 OpenAI 响应请求的中继逻辑，包括请求验证、敏感词检查、模型映射、配额管理等操作。
// 参数 c 为 gin 上下文，包含请求和响应相关信息。
// 返回值为 OpenAI 错误响应结构体指针，若请求处理过程中出现错误，返回相应的错误信息；若处理成功，返回 nil。
func ResponsesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	// 获取并验证请求体，将其解析为 OpenAIResponsesRequest 结构体
	req, err := getAndValidateResponsesRequest(c)
	if err != nil {
		common.LogError(c, fmt.Sprintf("getAndValidateResponsesRequest error: %s", err.Error()))
		return service.OpenAIErrorWrapperLocal(err, "invalid_responses_request", http.StatusBadRequest)
	}

	// 生成中继信息，包含请求相关的各种元数据
	relayInfo := relaycommon.GenRelayInfoResponses(c, req)

	// 检查是否需要进行敏感词检查
	if setting.ShouldCheckPromptSensitive() {
		// 执行敏感词检查
		sensitiveWords, err := checkInputSensitive(req, relayInfo)
		if err != nil {
			common.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(sensitiveWords, ", ")))
			return service.OpenAIErrorWrapperLocal(err, "check_request_sensitive_error", http.StatusBadRequest)
		}
	}

	// 进行模型映射，将请求中的模型名称映射为实际使用的模型
	err = helper.ModelMappedHelper(c, relayInfo, req)
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "model_mapped_error", http.StatusBadRequest)
	}

	// 检查上下文中是否已经存在输入令牌数
	if value, exists := c.Get("prompt_tokens"); exists {
		// 若存在，将其转换为整数并设置到中继信息中
		promptTokens := value.(int)
		relayInfo.SetPromptTokens(promptTokens)
	} else {
		// 若不存在，计算输入令牌数并设置到上下文中
		promptTokens := getInputTokens(req, relayInfo)
		c.Set("prompt_tokens", promptTokens)
	}

	// 计算模型价格，根据输入令牌数和最大输出令牌数计算所需费用
	priceData, err := helper.ModelPriceHelper(c, relayInfo, relayInfo.PromptTokens, int(req.MaxOutputTokens))
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "model_price_error", http.StatusInternalServerError)
	}
	// pre consume quota 预扣配额，根据价格数据决定是否需要预扣用户配额
	preConsumedQuota, userQuota, openaiErr := preConsumeQuota(c, priceData.ShouldPreConsumedQuota, relayInfo)
	if openaiErr != nil {
		return openaiErr
	}
	// 使用 defer 确保在函数返回前检查是否需要退还预扣的配额
	defer func() {
		if openaiErr != nil {
			// 若出现错误，退还预扣的配额
			returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		}
	}()

	// 根据 API 类型获取对应的适配器
	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return service.OpenAIErrorWrapperLocal(fmt.Errorf("invalid api type: %d", relayInfo.ApiType), "invalid_api_type", http.StatusBadRequest)
	}

	// 初始化适配器
	adaptor.Init(relayInfo)

	var requestBody io.Reader
	// 检查是否启用了透传请求
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		// 若启用，直接获取请求体
		body, err := common.GetRequestBody(c)
		if err != nil {
			return service.OpenAIErrorWrapperLocal(err, "get_request_body_error", http.StatusInternalServerError)
		}
		requestBody = bytes.NewBuffer(body)
	} else {
		// 若未启用，将请求转换为适配器所需的格式
		convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, relayInfo, *req)
		if err != nil {
			return service.OpenAIErrorWrapperLocal(err, "convert_request_error", http.StatusBadRequest)
		}
		// 将转换后的请求进行 JSON 序列化
		jsonData, err := json.Marshal(convertedRequest)
		if err != nil {
			return service.OpenAIErrorWrapperLocal(err, "marshal_request_error", http.StatusInternalServerError)
		}

		// apply param override 检查是否需要应用参数覆盖
		if len(relayInfo.ParamOverride) > 0 {
			// 将 JSON 数据反序列化为 map
			reqMap := make(map[string]interface{})
			err = json.Unmarshal(jsonData, &reqMap)
			if err != nil {
				return service.OpenAIErrorWrapperLocal(err, "param_override_unmarshal_failed", http.StatusInternalServerError)
			}
			// 应用参数覆盖
			for key, value := range relayInfo.ParamOverride {
				reqMap[key] = value
			}
			// 将更新后的 map 重新进行 JSON 序列化
			jsonData, err = json.Marshal(reqMap)
			if err != nil {
				return service.OpenAIErrorWrapperLocal(err, "param_override_marshal_failed", http.StatusInternalServerError)
			}
		}

		// 若开启调试模式，打印请求体
		if common.DebugEnabled {
			println("requestBody: ", string(jsonData))
		}
		requestBody = bytes.NewBuffer(jsonData)
	}

	var httpResp *http.Response
	// 调用适配器的 DoRequest 方法发送请求
	resp, err := adaptor.DoRequest(c, relayInfo, requestBody)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	// 从上下文中获取状态码映射字符串
	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		// 将响应转换为 http.Response 类型
		httpResp = resp.(*http.Response)

		// 检查响应状态码是否为 200 OK
		if httpResp.StatusCode != http.StatusOK {
			// 若不是，调用错误处理函数处理错误
			openaiErr = service.RelayErrorHandler(httpResp, false)
			// 重置状态码
			service.ResetStatusCode(openaiErr, statusCodeMappingStr)
			return openaiErr
		}
	}

	// 调用适配器的 DoResponse 方法处理响应
	usage, openaiErr := adaptor.DoResponse(c, httpResp, relayInfo)
	if openaiErr != nil {
		// 若处理响应失败，重置状态码并返回错误响应
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		return openaiErr
	}

	// 检查模型名称是否以 gpt-4o-audio 开头
	if strings.HasPrefix(relayInfo.OriginModelName, "gpt-4o-audio") {
		// 若是音频模型，调用音频配额扣除函数
		service.PostAudioConsumeQuota(c, relayInfo, usage.(*dto.Usage), preConsumedQuota, userQuota, priceData, "")
	} else {
		// 若不是音频模型，调用普通配额扣除函数
		postConsumeQuota(c, relayInfo, usage.(*dto.Usage), preConsumedQuota, userQuota, priceData, "")
	}

	return nil
}
