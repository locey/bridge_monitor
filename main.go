package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/sirupsen/logrus"

	"go.mongodb.org/mongo-driver/mongo"
	"meson-monitor/database"
	"meson-monitor/bot"
)

type Config struct {
	Main struct {
		WalletAddress string `json:"walletAddress"`
		PrivateKey    string `json:"privateKey"`
		CheckTime     int    `json:"check_time"`
		BotToken      string `json:"botToken"`
		ChatID        int64  `json:"chatID"`
		LarkBotURL    string `json:"lark_bot"`
		PostgresURI   string `json:"postgresURI"`
	} `json:"main"`
	Chains map[string]struct {
		RpcUrl        string `json:"rpcUrl"`
		MesonContract string `json:"mesonContract"`
		MesonIndex    uint8  `json:"mesonIndex"`
		TokenDecimal  uint8  `json:"tokendecimal"`
		TokenContract string `json:"tokenContract"`
	} `json:"chains"`
}


var (
	telegramBot *bot.TelegramBot // 全局 TelegramBot 实例
	larkBot     *bot.LarkBot    // 全局 LarkBot 实例
	contractABI = `[{"anonymous":false,"inputs":[{"indexed":true,"name":"reqId","type":"bytes32"},{"indexed":true,"name":"recipient","type":"address"}],"name":"TokenMintExecuted","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"name":"reqId","type":"bytes32"},{"indexed":true,"name":"proposer","type":"address"}],"name":"TokenBurnExecuted","type":"event"}]`
)

// loadConfig 读取并解析配置文件
// 该函数接受一个文件名字符串参数，并返回一个指向 Config 结构体的指针和一个错误值
func loadConfig(filename string) (*Config, error) {
	// 打开指定的配置文件
	file, err := os.Open(filename)
	if err != nil {
		// 如果打开文件失败，返回 nil 和错误信息
		return nil, err
	}
	defer file.Close() // 确保在函数结束时关闭文件

	var config Config // 创建一个 Config 结构体实例来存储解析结果

	// 创建一个 JSON 解码器，读取文件内容
	decoder := json.NewDecoder(file)
	// 将文件内容解码到 Config 结构体实例中
	err = decoder.Decode(&config)
	if err != nil {
		// 如果解码失败，返回 nil 和错误信息
		return nil, err
	}

	// 返回解析后的 Config 结构体指针和 nil 错误
	return &config, nil
}


// meson_event 验证 Meson 事件
func meson_event(actionA, actionB string) bool {
	return (actionA == "TokenBurnExecuted" && actionB == "TokenMintExecuted") ||
		(actionA == "TokenMintExecuted" && actionB == "TokenBurnExecuted")
}


func sendNotification(messageType, message string) {
	if messageType == "Error" {
		message = "Error: " + message
	} else if messageType == "Information" {
		message = "Information: " + message
	}

	telegramErr := telegramBot.SendMessage(message, "HTML")
	if telegramErr != nil {
		logrus.Errorf("Failed to send Telegram message: %v", telegramErr)
	}

	larkErr := larkBot.SendMessage(message)
	if larkErr != nil {
		logrus.Errorf("Failed to send Lark message: %v", larkErr)
	}
}


func meson_handle(reqID, chainName, eventName string, createdTime int64, amount float64, txHash string) error {
	// 查询数据库中是否已存在该 reqID 的文档
	existingMeson, err := database.FindMesonByReqID(reqID)
	if err != nil && err != mongo.ErrNoDocuments {
		// 如果查询过程中出现错误（且不是没有文档错误），记录错误并返回
		logrus.Errorf("Failed to query Meson by ReqID: %v", err)
		return fmt.Errorf("failed to query Meson by ReqID: %v", err)
	}

	if existingMeson != nil {
		if existingMeson.ChainB != "" {
			// 构建错误消息
			message := fmt.Sprintf(
				"\nMeson same reqId: ChainB already has a value\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
				existingMeson.ReqID, existingMeson.ChainA, existingMeson.ChainB, existingMeson.Timestamp, existingMeson.AmountA, existingMeson.AmountB, existingMeson.ActionA, existingMeson.ActionB, existingMeson.TxHashA, existingMeson.TxHashB, existingMeson.IsCheck)

			// 发送错误消息
			sendNotification("Error", message)

			logrus.Errorf("ChainB already has a value for ReqID: %s", reqID)
			return fmt.Errorf("error: ChainB already has a value")
		} else {
			// 如果文档存在，且 ChainB 字段为空，更新文档
			existingMeson.ChainB = chainName
			existingMeson.AmountB = amount
			existingMeson.ActionB = eventName
			existingMeson.TxHashB = txHash
			existingMeson.IsCheck = existingMeson.AmountA == existingMeson.AmountB
			err := database.UpdateMeson(existingMeson)
			if err != nil {
				// 如果更新文档失败，记录错误并返回
				logrus.Errorf("Failed to update Meson: %v", err)
				return fmt.Errorf("failed to update Meson: %v", err)
			}
			logrus.Info("Updated Meson document with ChainB information.")

			// 验证动作，必须是一个 burn，另一个是 mint
			if !meson_event(existingMeson.ActionA, existingMeson.ActionB) {
				// 构建错误消息
				message := fmt.Sprintf(
					"\nMeson same event!\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
					existingMeson.ReqID, existingMeson.ChainA, existingMeson.ChainB, existingMeson.Timestamp, existingMeson.AmountA, existingMeson.AmountB, existingMeson.ActionA, existingMeson.ActionB, existingMeson.TxHashA, existingMeson.TxHashB, existingMeson.IsCheck)

				// 发送错误消息
				sendNotification("Error", message)

				logrus.Errorf("Meson event validation failed for ReqID: %s", reqID)
				return fmt.Errorf("error: meson event validation failed: actionA and actionB must be one TokenBurnExecuted and one TokenMintExecuted")
			}

			// 验证数额，必须两个数额是一样的
			if !existingMeson.IsCheck {
				message := fmt.Sprintf(
					"\nAmounts do not match!\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
					existingMeson.ReqID, existingMeson.ChainA, existingMeson.ChainB, existingMeson.Timestamp, existingMeson.AmountA, existingMeson.AmountB, existingMeson.ActionA, existingMeson.ActionB, existingMeson.TxHashA, existingMeson.TxHashB, existingMeson.IsCheck)

				// 发送错误消息
				sendNotification("Error", message)

				logrus.Errorf("Amounts do not match for ReqID: %s", reqID)
				return fmt.Errorf("error: Amounts do not match.")
			}

			// 构建成功消息
			message := fmt.Sprintf(
				"\nCross-chain success!\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
				existingMeson.ReqID, existingMeson.ChainA, existingMeson.ChainB, existingMeson.Timestamp, existingMeson.AmountA, existingMeson.AmountB, existingMeson.ActionA, existingMeson.ActionB, existingMeson.TxHashA, existingMeson.TxHashB, existingMeson.IsCheck)

			// 发送信息消息
			sendNotification("Information", message)

			logrus.Info("Amounts match for ReqID: ", reqID)
		}
	} else {
		// 如果文档不存在，插入新文档
		meson := database.Meson{
			ReqID:     reqID,
			ChainA:    chainName,
			Timestamp: createdTime,
			AmountA:   amount,
			ActionA:   eventName,
			TxHashA:   txHash,
			IsCheck:   false,
		}
		err = database.InsertMeson(meson)
		if err != nil {
			// 如果插入文档失败，记录错误并返回
			logrus.Errorf("Failed to insert Meson: %v", err)
			return fmt.Errorf("failed to insert Meson: %v", err)
		}
		logrus.Info("Inserted new Meson document with ID: ", reqID)
	}

	return nil
}



// processEvent 处理事件的公共逻辑
// 该函数接受链名称、事件名称、请求 ID、地址、Meson 索引和代币小数位数作为参数
func processEvent(chainName, eventName string, reqID common.Hash, address common.Address, txHash common.Hash, mesonIndex uint8, tokenDecimal uint8) {
	// 处理 ReqID，将其转换为 *big.Int 类型
	reqIdBigInt := new(big.Int).SetBytes(reqID.Bytes())

	// 检查 tokenIndex 是否匹配已知的 token index
	if isMyToken(reqIdBigInt, mesonIndex) {
		// 获取 amount，从 ReqID 中提取金额
		amount, err := getAmountFromReqID(reqIdBigInt, tokenDecimal)
		if err != nil {
			// 如果提取金额失败，输出错误信息并返回
			logrus.Errorf("Failed to get amount from ReqID: %v", err)
			return
		}

		// 获取 createdTime，从 ReqID 中提取创建时间
		createdTime := getCreatedTimeFromReqID(reqIdBigInt)
		// 格式化创建时间为 RFC3339 格式
		createdTimeFormatted := time.Unix(int64(createdTime), 0).UTC().Format(time.RFC3339)

		// 输出事件信息
		logrus.Infof("Event: %s", eventName)
		logrus.Infof("ReqID: %s", reqID.Hex())
		logrus.Infof("Chain: %s", chainName)
		logrus.Infof("CreatedTime: %d (%s)", createdTime, createdTimeFormatted)
		logrus.Infof("Amount: %d", amount)
		logrus.Infof("Token Index matches the known token index %d", mesonIndex)
		logrus.Infof("Transaction Hash: %s", txHash.Hex())

		// 保存或更新 Meson 文档
		err = meson_handle(reqID.Hex(), chainName, eventName, int64(createdTime), float64(amount), txHash.Hex())
		if err != nil {
			logrus.Errorf("Database operation failed: %v", err)
		}
	}
}


// listenEvents 启动一个无限循环监听指定链上的事件
// 该函数接受一个 WaitGroup 指针、链名称、RPC URL、合约地址、Meson 索引和代币小数位数作为参数
func listenEvents(wg *sync.WaitGroup, chainName, rpcUrl, tokenContract string, mesonIndex uint8, tokenDecimal uint8) {
	defer wg.Done() // 在函数结束时调用 Done 方法以通知 WaitGroup 当前协程已完成

	for {
		// 创建一个带取消功能的上下文
		ctx, cancel := context.WithCancel(context.Background())

		// 连接到以太坊客户端并监听事件
		err := connectAndListen(ctx, chainName, rpcUrl, tokenContract, mesonIndex, tokenDecimal)
		if err != nil {
			// 如果连接或监听过程中出现错误，输出错误信息并在 5 秒后重试
			logrus.Errorf("Error in connectAndListen: %v. Retrying in 5 seconds...\n", err)
			time.Sleep(5 * time.Second)
		}

		// 确保在每次重试之前取消先前的上下文
		cancel()
	}
}


// connectAndListen 连接到以太坊客户端并监听指定合约的事件
// 该函数接受上下文、链名称、RPC URL、合约地址、Meson 索引和代币小数位数作为参数
// 返回一个错误值
func connectAndListen(ctx context.Context, chainName, rpcUrl, tokenContract string, mesonIndex uint8, tokenDecimal uint8) error {
	// 连接到以太坊客户端
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		logrus.Errorf("Failed to connect to the Ethereum client: %v", err)
		return fmt.Errorf("Failed to connect to the Ethereum client: %v", err)
	}
	defer client.Close() // 确保在函数结束时关闭客户端连接

	// 解析合约的 ABI
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		logrus.Errorf("Failed to parse contract ABI: %v", err)
		return fmt.Errorf("Failed to parse contract ABI: %v", err)
	}

	// 设置过滤器查询，指定要监听的合约地址
	query := ethereum.FilterQuery{
		Addresses: []common.Address{common.HexToAddress(tokenContract)},
	}

	logs := make(chan types.Log) // 创建一个通道用于接收日志
	// 订阅过滤器日志
	sub, err := client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		logrus.Errorf("Failed to subscribe to logs: %v", err)
		return fmt.Errorf("Failed to subscribe to logs: %v", err)
	}
	defer sub.Unsubscribe() // 确保在函数结束时取消订阅

	for {
		select {
		case <-ctx.Done():
			// 如果上下文被取消，返回错误
			logrus.Warn("Context cancelled")
			return fmt.Errorf("Context cancelled")
		case err := <-sub.Err():
			// 如果订阅过程中出现错误，返回错误
			logrus.Errorf("Subscription error: %v", err)
			return fmt.Errorf("Subscription error: %v", err)
		case vLog := <-logs:
			// 处理接收到的日志
			// 打印交易哈希值
			logrus.Infof("Transaction Hash: %s", vLog.TxHash.Hex())

			// 根据事件的 topic[0] 进行匹配，处理不同的事件类型
			switch vLog.Topics[0].Hex() {
			case parsedABI.Events["TokenMintExecuted"].ID.Hex():
				// 处理 TokenMintExecuted 事件
				event := struct {
					ReqID     common.Hash
					Recipient common.Address
				}{
					ReqID:     vLog.Topics[1], // 从 Topics 中提取 reqId
					Recipient: common.HexToAddress(vLog.Topics[2].Hex()), // 从 Topics 中提取 recipient
				}
				processEvent(chainName, "TokenMintExecuted", event.ReqID, event.Recipient, vLog.TxHash, mesonIndex, tokenDecimal)

			case parsedABI.Events["TokenBurnExecuted"].ID.Hex():
				// 处理 TokenBurnExecuted 事件
				event := struct {
					ReqID    common.Hash
					Proposer common.Address
				}{
					ReqID:    vLog.Topics[1], // 从 Topics 中提取 reqId
					Proposer: common.HexToAddress(vLog.Topics[2].Hex()), // 从 Topics 中提取 proposer
				}
				processEvent(chainName, "TokenBurnExecuted", event.ReqID, event.Proposer, vLog.TxHash, mesonIndex, tokenDecimal)
			}
		}
	}
}


// checkDatabase 定期检查数据库中 is_check 为 false 的 Meson 文档
// 该函数接受一个 WaitGroup 指针和一个检查间隔时间（毫秒）作为参数
func checkDatabase(wg *sync.WaitGroup, checkTime int) {
	defer wg.Done() // 在函数结束时，调用 Done 方法以通知 WaitGroup 当前协程已完成

	// 创建一个新的 Ticker，每隔 checkTime 毫秒触发一次
	ticker := time.NewTicker(time.Duration(checkTime) * time.Millisecond)
	defer ticker.Stop() // 确保在函数结束时停止 Ticker

	for range ticker.C {
		// 查询 is_check 为 false 的文档
		results, err := database.FindUncheckedMesons()
		if err != nil {
			// 如果查询失败，输出错误信息并继续下一个周期
			logrus.Errorf("Failed to find unchecked Mesons: %v", err)
			continue
		}

		if len(results) > 0 {
			// 如果有未检查的 Meson 文档，输出信息
			logrus.Info("Unchecked Mesons:")
			for _, meson := range results {
				// 构建消息字符串，包含 Meson 文档的详细信息
				message := fmt.Sprintf(
					"Error: \ncross-chain Time Out!\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
					meson.ReqID, meson.ChainA, meson.ChainB, meson.Timestamp, meson.AmountA, meson.AmountB, meson.ActionA, meson.ActionB, meson.TxHashA, meson.TxHashB, meson.IsCheck)
				logrus.Info(message)

				// 发送消息到 Telegram
				err := telegramBot.SendMessage(message, "HTML")
				if err != nil {
					// 如果发送 Telegram 消息失败，输出错误信息
					logrus.Errorf("Failed to send Telegram message: %v", err)
				}

				// 发送消息到 Lark
				larkErr := larkBot.SendMessage(message)
				if larkErr != nil {
					// 如果发送 Lark 消息失败，输出错误信息
					logrus.Errorf("Failed to send Lark message: %v", larkErr)
				}
			}
		}
	}
}


// isMyToken 检查 tokenIndex 是否匹配已知的 token index
// 该函数接受一个 *big.Int 类型的 reqId 和一个 uint8 类型的 myTokenIndex 作为参数
// 返回一个布尔值，表示 tokenIndex 是否匹配 myTokenIndex
func isMyToken(reqId *big.Int, myTokenIndex uint8) bool {
	// 从 reqId 中提取 tokenIndex，方法是将 reqId 右移 192 位，然后取最低 8 位
	tokenIndex := uint8(new(big.Int).Rsh(reqId, 192).Uint64() & 0xFF)
	// 检查提取的 tokenIndex 是否等于 myTokenIndex
	return tokenIndex == myTokenIndex
}

// getAmountFromReqID 从 reqId 中提取金额
// 该函数接受一个 *big.Int 类型的 reqId 和一个 uint8 类型的 decimals 作为参数
// 返回一个 uint64 类型的金额和一个错误值
func getAmountFromReqID(reqId *big.Int, decimals uint8) (uint64, error) {
	// 从 reqId 中提取金额，方法是将 reqId 右移 128 位，然后取最低 64 位
	amount := new(big.Int).Rsh(reqId, 128).Uint64() & 0xFFFFFFFFFFFFFFFF
	if amount == 0 {
		// 如果金额为零，记录错误并返回
		logrus.Errorf("amount must be greater than zero")
		return 0, fmt.Errorf("amount must be greater than zero")
	}

	// 处理小数点位置
	if decimals > 6 {
		// 如果小数位数大于 6，乘以 10^(decimals-6)
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-6)), nil).Uint64()
		amount *= multiplier
	} else {
		// 如果小数位数小于等于 6，除以 10^(6-decimals)
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(6-decimals)), nil).Uint64()
		amount /= divisor
	}

	return amount, nil
}

// getCreatedTimeFromReqID 从 reqId 中提取 createdTime
// 该函数接受一个 *big.Int 类型的 reqId 作为参数
// 返回一个 uint64 类型的 createdTime
func getCreatedTimeFromReqID(reqId *big.Int) uint64 {
	// 将 reqId 右移 208 位，提取前 40 位作为 createdTime
	createdTime := new(big.Int).Rsh(reqId, 208).Uint64() & 0xFFFFFFFFFF
	return createdTime
}

// InitLogger 初始化日志记录器
func InitLogger() {
	// 设置日志格式
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// 检查并创建日志文件
	file, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		logrus.SetOutput(file)
	} else {
		logrus.SetOutput(os.Stdout)
		logrus.Warn("Failed to log to file, using default stderr")
	}

	// 设置日志级别
	logrus.SetLevel(logrus.InfoLevel)
}


func main() {

	// 初始化日志记录器
	InitLogger()

	// 读取配置文件
	// 调用 loadConfig 函数读取并解析配置文件 "config.json"
	config, err := loadConfig("config.json")
	if err != nil {
		// 如果读取或解析配置文件失败，记录错误并退出程序
		logrus.Fatalf("Failed to load config file: %v", err)
	}

	// 初始化 PostgreSQL 数据库连接
	err = database.Connect(config.Main.PostgresURI)
	if err != nil {
		logrus.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer database.Disconnect()
	// 初始化数据库
	err = database.InitDatabase()
	if err != nil {
		logrus.Fatalf("Failed to initialize PostgreSQL: %v", err)
	}

	// 初始化 Telegram 和 Lark 机器人
	// 使用配置文件中的参数创建 Telegram 和 Lark 机器人实例
	telegramBot = bot.NewTelegramBot(config.Main.BotToken, config.Main.ChatID)
	larkBot = bot.NewLarkBot(config.Main.LarkBotURL)

	// 使用 WaitGroup 来等待监听协程完成
	var wg sync.WaitGroup

	// 启动数据库检查协程
	wg.Add(1) // 增加 WaitGroup 计数
	// 启动一个新的协程执行 checkDatabase 函数
	go checkDatabase(&wg, config.Main.CheckTime)

	// 遍历所有链配置并启动监听协程
	// 遍历配置文件中的所有链配置
	for chainName, chainConfig := range config.Chains {
		logrus.Infof("Starting listener for chain: %s", chainName)
		wg.Add(1) // 增加 WaitGroup 计数
		// 启动一个新的协程执行 listenEvents 函数
		go listenEvents(&wg, chainName, chainConfig.RpcUrl, chainConfig.MesonContract, chainConfig.MesonIndex, chainConfig.TokenDecimal)
	}

	// 等待所有协程完成（实际上不会，因为协程中有无限循环）
	wg.Wait()
}


