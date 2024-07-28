package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/sirupsen/logrus"

	"meson-monitor/bot"
	"meson-monitor/database"

)

type Config struct {
	Main struct {
		WalletAddress string   `json:"walletAddress"`
		PrivateKey    string   `json:"privateKey"`
		CheckTime     int      `json:"check_time"`
		BotToken      string   `json:"botToken"`
		ChatIDs       []int64  `json:"chatIDs"`
		LarkBotURL    string   `json:"lark_bot"`
		PostgresURI   string   `json:"postgresURI"`
	} `json:"main"`
	Chains map[string]struct {
		RpcUrl        string `json:"rpcUrl"`
		MesonContract string `json:"mesonContract"`
		MesonIndex    uint8  `json:"mesonIndex"`
		TokenDecimal  uint8  `json:"tokendecimal"`
		StartBlock    uint64 `json:"startBlock"`
		TokenContract string `json:"tokenContract"`
	} `json:"chains"`
}

var (
	telegramBot *bot.TelegramBot // 全局 TelegramBot 实例
	larkBot     *bot.LarkBot     // 全局 LarkBot 实例
	contractABI = `[{"anonymous":false,"inputs":[{"indexed":true,"name":"reqId","type":"bytes32"},{"indexed":true,"name":"recipient","type":"address"}],"name":"TokenMintExecuted","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"name":"reqId","type":"bytes32"},{"indexed":true,"name":"proposer","type":"address"}],"name":"TokenBurnExecuted","type":"event"}]`
)

const (
	lastBlockDir = "last_block"
	blockStep    = 5000
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

// 格式化数字为千分位
func formatWithCommas(number float64) string {
	return addCommas(strconv.FormatInt(int64(number), 10))
}

// 添加逗号作为千分位分隔符
func addCommas(numStr string) string {
	n := len(numStr)
	if n <= 3 {
		return numStr
	}
	rem := n % 3
	if rem > 0 {
		return numStr[:rem] + "," + addCommas(numStr[rem:])
	}
	return numStr[:3] + "," + addCommas(numStr[3:])
}

// 构建消息的函数
func constructMessage(timestamp int64, chainA, actionA string, amountA float64, txHashA string, chainB, actionB string, amountB float64, txHashB string) {
	var fromChain, toChain, fromAction, toAction string
	var fromAmount, toAmount float64
	var fromTxHash, toTxHash string

	if actionA == "TokenBurnExecuted" {
		fromChain, fromAction, fromAmount, fromTxHash = chainA, "Burn", amountA, txHashA
		toChain, toAction, toAmount, toTxHash = chainB, "Mint", amountB, txHashB
	} else {
		fromChain, fromAction, fromAmount, fromTxHash = chainB, "Burn", amountB, txHashB
		toChain, toAction, toAmount, toTxHash = chainA, "Mint", amountA, txHashA
	}

	telegramMessage := fmt.Sprintf(
		"<b>*****❗️❗️Bridge data anomaly❗️❗️*****</b>\n<b>Time:</b> %s\n\n<b>From:</b> %s <b>%s</b> [%s]\n<b>To:</b> %s <b>%s</b> [%s]\n\n<b>Tx hash (From):</b> %s\n<b>Tx hash (To):</b> %s\n",
		time.Unix(timestamp, 0).UTC().Format(time.RFC3339),
		fromChain, fromAction, formatWithCommas(fromAmount),
		toChain, toAction, formatWithCommas(toAmount),
		fromTxHash,
		toTxHash,
	)

	larkTitle := "*****❗️❗️Bridge data anomaly❗️❗️*****"
	larkTime := time.Unix(timestamp, 0).UTC().Format(time.RFC3339)
	larkFrom := fmt.Sprintf("%s **%s** [%s]", fromChain, fromAction, formatWithCommas(fromAmount))
	larkTo := fmt.Sprintf("%s **%s** [%s]", toChain, toAction, formatWithCommas(toAmount))
	larkTxHashFrom := fromTxHash
	larkTxHashTo := toTxHash

	// 发送消息到 Telegram
	telegramErr := telegramBot.SendMessage(telegramMessage, "HTML")
	if telegramErr != nil {
		logrus.Errorf("Failed to send Telegram message: %v", telegramErr)
	}

	// 发送消息到 Lark
	larkErr := larkBot.SendMessage(larkTitle, larkTime, larkFrom, larkTo, larkTxHashFrom, larkTxHashTo)
	if larkErr != nil {
		logrus.Errorf("Failed to send Lark message: %v", larkErr)
	}
}



func meson_handle(reqID, chainName, eventName string, createdTime int64, amount float64, txHash string) error {
	// 查询数据库中是否已存在该 reqID 的文档
	existingMeson, err := database.FindMesonByReqID(reqID)
	if err != nil{
		// 如果查询过程中出现错误（且不是没有文档错误），记录错误并返回
		logrus.Errorf("Failed to query Meson by ReqID: %v", err)
		return fmt.Errorf("failed to query Meson by ReqID: %v", err)
	}

	if existingMeson != nil {
		if existingMeson.ChainB != "" {
			// 构建错误消息
			constructMessage (
				existingMeson.Timestamp,
				existingMeson.ChainA, existingMeson.ActionA, existingMeson.AmountA, existingMeson.TxHashA,
				existingMeson.ChainB, existingMeson.ActionB, existingMeson.AmountB, existingMeson.TxHashB,
			)

			// 发送错误消息
			//sendNotification("Error", message)

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
				constructMessage(
					existingMeson.Timestamp,
					existingMeson.ChainA, existingMeson.ActionA, existingMeson.AmountA, existingMeson.TxHashA,
					existingMeson.ChainB, existingMeson.ActionB, existingMeson.AmountB, existingMeson.TxHashB,
				)

				// 发送错误消息
				//sendNotification("Error", message)

				logrus.Errorf("Meson event validation failed for ReqID: %s", reqID)
				return fmt.Errorf("error: meson event validation failed: actionA and actionB must be one TokenBurnExecuted and one TokenMintExecuted")
			}

			// 验证数额，必须两个数额是一样的
			if !existingMeson.IsCheck {
				constructMessage(
					existingMeson.Timestamp,
					existingMeson.ChainA, existingMeson.ActionA, existingMeson.AmountA, existingMeson.TxHashA,
					existingMeson.ChainB, existingMeson.ActionB, existingMeson.AmountB, existingMeson.TxHashB,
				)

				// 发送错误消息
				//sendNotification("Error", message)

				logrus.Errorf("Amounts do not match for ReqID: %s", reqID)
				return fmt.Errorf("error: Amounts do not match.")
			}

			// 成功消息通过日志打印，不发送通知
			logrus.Infof(
				"Cross-chain success!\nReqID: %s\nChainA: %s\nChainB: %s\nTimestamp: %d\nAmountA: %f\nAmountB: %f\nActionA: %s\nActionB: %s\nTxHashA: %s\nTxHashB: %s\nIsCheck: %t\n",
				existingMeson.ReqID, existingMeson.ChainA, existingMeson.ChainB, existingMeson.Timestamp, existingMeson.AmountA, existingMeson.AmountB, existingMeson.ActionA, existingMeson.ActionB, existingMeson.TxHashA, existingMeson.TxHashB, existingMeson.IsCheck,
			)
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
func listenEvents(wg *sync.WaitGroup, chainName, rpcUrl, tokenContract string, mesonIndex uint8, tokenDecimal uint8, startBlock uint64) {
	defer wg.Done() // 在函数结束时调用 Done 方法以通知 WaitGroup 当前协程已完成

	for {
		// 创建一个带取消功能的上下文
		ctx, cancel := context.WithCancel(context.Background())

		// 连接到以太坊客户端并监听事件
		err := connectAndListen(ctx, chainName, rpcUrl, tokenContract, mesonIndex, tokenDecimal, startBlock)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"ChainName": chainName,
				"Error":     err,
			}).Error("Error in connectAndListen. Retrying in 30 seconds...\n")
			time.Sleep(30 * time.Second)
		}

		// 确保在每次重试之前取消先前的上下文
		cancel()
	}
}

// getLatestBlockNumber 获取当前链的最新区块号
func getLatestBlockNumber(client *ethclient.Client) (uint64, error) {
	header, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		logrus.Errorf("Failed to get latest block header: %v", err)
		return 0, err
	}
	logrus.Infof("Latest block number: %d", header.Number.Uint64())
	return header.Number.Uint64(), nil
}


func getLastBlockNumber(chainName string, client *ethclient.Client, contractAddress common.Address, startBlock uint64) (uint64, error) {
	filename := filepath.Join("last_block", chainName+".txt")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		logrus.Infof("Using startBlock from config for chain: %s", chainName)
		return startBlock, nil // 从配置文件中的起始区块号开始
	}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		logrus.Errorf("Failed to read last block file: %v", err)
		return 0, err
	}
	var blockNumber uint64
	err = json.Unmarshal(data, &blockNumber)
	if err != nil {
		logrus.Errorf("Failed to unmarshal last block number: %v", err)
		return 0, err
	}
	logrus.Infof("Last block number for chain %s: %d", chainName, blockNumber)
	return blockNumber, nil
}



func saveLastBlockNumber(chainName string, blockNumber uint64) error {
	filename := filepath.Join("last_block", chainName+".txt")
	data, err := json.Marshal(blockNumber)
	if err != nil {
		logrus.Errorf("Failed to marshal block number: %v", err)
		return err
	}
	err = ioutil.WriteFile(filename, data, 0644)
	if err != nil {
		logrus.Errorf("Failed to write last block number to file: %v", err)
	}
	logrus.Infof("Saved last block number %d for chain %s to file: %s", blockNumber, chainName, filename)
	return err
}


// connectAndListen 连接到以太坊客户端并监听指定合约的事件
// 该函数接受上下文、链名称、RPC URL、合约地址、Meson 索引和代币小数位数作为参数
// 返回一个错误值
func connectAndListen(ctx context.Context, chainName, rpcUrl, tokenContract string, mesonIndex uint8, tokenDecimal uint8, startBlockConfig uint64) error {
	logrus.Infof("Connecting to RPC URL: %s", rpcUrl)
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		logrus.Errorf("Failed to connect to the Ethereum client: %v", err)
		return fmt.Errorf("Failed to connect to the Ethereum client: %v", err)
	}
	defer client.Close()

	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		logrus.Errorf("Failed to parse contract ABI: %v", err)
		return fmt.Errorf("Failed to parse contract ABI: %v", err)
	}

	contractAddress := common.HexToAddress(tokenContract)
	startBlock, err := getLastBlockNumber(chainName, client, contractAddress, startBlockConfig)
	if err != nil {
		logrus.Errorf("Failed to get last block number: %v", err)
		return fmt.Errorf("Failed to get last block number: %v", err)
	}

	for {
		latestBlock, err := getLatestBlockNumber(client)
		logrus.Infof("Chain name: %s, Latest block: %d", chainName, latestBlock)
		if err != nil {
			logrus.Errorf("Failed to get latest block number: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// 确保最新区块号大于上次检查的区块号100以上
		if latestBlock <= startBlock+100 {
			logrus.Infof("Latest block (%d) is not greater than start block (%d) by at least 100. Waiting...", latestBlock, startBlock)
			time.Sleep(600 * time.Second)
			continue
		}

		endBlock := startBlock + blockStep
		if endBlock > latestBlock {
			endBlock = latestBlock
		}

		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(int64(startBlock)),
			ToBlock:   big.NewInt(int64(endBlock)),
			Addresses: []common.Address{contractAddress},
		}

		logs, err := client.FilterLogs(ctx, query)
		if err != nil {
			logrus.Errorf("Failed to filter logs: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, vLog := range logs {
			logrus.Infof("Transaction Hash: %s", vLog.TxHash.Hex())

			switch vLog.Topics[0].Hex() {
			case parsedABI.Events["TokenMintExecuted"].ID.Hex():
				event := struct {
					ReqID     common.Hash
					Recipient common.Address
				}{
					ReqID:     vLog.Topics[1],
					Recipient: common.HexToAddress(vLog.Topics[2].Hex()),
				}
				processEvent(chainName, "TokenMintExecuted", event.ReqID, event.Recipient, vLog.TxHash, mesonIndex, tokenDecimal)

			case parsedABI.Events["TokenBurnExecuted"].ID.Hex():
				event := struct {
					ReqID    common.Hash
					Proposer common.Address
				}{
					ReqID:    vLog.Topics[1],
					Proposer: common.HexToAddress(vLog.Topics[2].Hex()),
				}
				processEvent(chainName, "TokenBurnExecuted", event.ReqID, event.Proposer, vLog.TxHash, mesonIndex, tokenDecimal)
			}
		}

		startBlock = endBlock + 1
		err = saveLastBlockNumber(chainName, startBlock)
		if err != nil {
			logrus.Errorf("Failed to save last block number: %v", err)
		}
		time.Sleep(5 * time.Second) // 延迟一段时间后继续查询
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
				constructMessage(
					meson.Timestamp,
					meson.ChainA, meson.ActionA, meson.AmountA, meson.TxHashA,
					meson.ChainB, meson.ActionB, meson.AmountB, meson.TxHashB,
				)
				//logrus.Info(message)

				// 使用 sendNotification 函数统一发送消息
				//sendNotification("Error", message)
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
	telegramBot = bot.NewTelegramBot(config.Main.BotToken, config.Main.ChatIDs)
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
		go listenEvents(&wg, chainName, chainConfig.RpcUrl, chainConfig.MesonContract, chainConfig.MesonIndex, chainConfig.TokenDecimal, chainConfig.StartBlock)
	}

	// 等待所有协程完成（实际上不会，因为协程中有无限循环）
	wg.Wait()
}
