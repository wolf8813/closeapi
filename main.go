package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"
	"one-api/common"
	"one-api/common2"
	"one-api/constant"
	"one-api/controller"
	"one-api/middleware"
	"one-api/model"
	"one-api/router"
	"one-api/service"
	"one-api/setting/ratio_setting"
	"os"
	"strconv"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

//go:embed web/dist
var buildFS embed.FS

//go:embed web/dist/index.html
var indexPage []byte

func main() {

	err := InitResources()
	if err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}

	common.SetupLogger()
	common.SysLog("New API " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	defer func() {
		err := model.CloseDB()
		if err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}()

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysError(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, fixErr := model.FixAbility()
					if fixErr != nil {
						common.SysError(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			model.InitChannelCache()
		}()

		go model.SyncChannelCache(common.SyncFrequency)
	}

	// 热更新配置
	go model.SyncOptions(common.SyncFrequency)

	// 数据看板
	go model.UpdateQuotaData()

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			common.FatalLog("failed to parse CHANNEL_UPDATE_FREQUENCY: " + err.Error())
		}
		go controller.AutomaticallyUpdateChannels(frequency)
	}
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err != nil {
			common.FatalLog("failed to parse CHANNEL_TEST_FREQUENCY: " + err.Error())
		}
		go controller.AutomaticallyTestChannels(frequency)
	}
	if common.IsMasterNode && constant.UpdateTask {
		gopool.Go(func() {
			controller.UpdateMidjourneyTaskBulk()
		})
		gopool.Go(func() {
			controller.UpdateTaskBulk()
		})
	}
	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		model.InitBatchUpdater()
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		gopool.Go(func() {
			log.Println(http.ListenAndServe("0.0.0.0:8005", nil))
		})
		go common.Monitor()
		common.SysLog("pprof enabled")
	}

	// 在已有初始化代码后添加
	if os.Getenv("CHANNEL_SYNC_ENABLED") == "true" {
		go controller.StartChannelSyncService()
		common.SysLog("启动频道同步服务")
	}

	// Initialize HTTP server
	server := gin.New()
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysError(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	middleware.SetUpLogger(server)
	// Initialize session store
	store := cookie.NewStore([]byte(common.SessionSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   2592000, // 30 days
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
	})
	server.Use(sessions.Sessions("session", store))

	router.SetRouter(server, buildFS, indexPage)
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}
	err = server.Run(":" + port)
	if err != nil {
		common.FatalLog("failed to start HTTP server: " + err.Error())
	}

}

// InitResources 初始化应用运行所需的各种资源，包括环境变量、模型设置、数据库、Redis 等。
// 若初始化过程中出现错误，将返回相应的错误信息。
func InitResources() error {
	// 尝试加载 .env 文件中的环境变量
	err := godotenv.Load(".env")
	if err != nil {
		// 若未找到 .env 文件，输出提示信息，使用系统默认环境变量
		common.SysLog("未找到 .env 文件，使用默认环境变量，如果需要，请创建 .env 文件并设置相关变量")
		common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
	}

	// 加载环境变量，将环境变量的值初始化到 common 包的相关变量中
	common.InitEnv()

	// 初始化模型比率设置，为后续模型相关操作提供基础配置
	ratio_setting.InitRatioSettings()

	// 初始化 HTTP 客户端，用于后续的网络请求操作
	service.InitHttpClient()

	// 初始化令牌编码器，用于对令牌进行编码解码操作
	service.InitTokenEncoders()

	// 初始化 SQL 数据库连接，若初始化失败将输出致命错误日志并返回错误
	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}

	// 检查数据库是否完成初始设置，确保数据库状态正常
	model.CheckSetup()

	// 初始化选项映射，从数据库中加载配置选项，应在数据库初始化之后执行
	model.InitOptionMap()

	// 初始化模型定价信息，从数据库或其他数据源获取模型定价
	model.GetPricing()

	// 初始化日志数据库连接，若初始化失败将返回错误
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// 初始化 Redis 客户端，若初始化失败将返回错误
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	// 初始化 MinIO 客户端，若初始化失败将返回错误
	err = common2.InitIdriveClient()
	if err != nil {
		return err
	}

	return nil
}
