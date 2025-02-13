package main

import (
	"bestrui/wechatpush/mail"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eatmoreapple/openwechat"
)

// 定义配置结构体
type Config struct {
	BlockedGroups []string `json:"blockedGroups"`
}

var config Config
var allGroups map[string]bool
var bot *openwechat.Bot     // 将 bot 声明为全局变量
var qrCodeUUID string       // 用于存储二维码 UUID
var qrCodeUrl string        // 用于存储二维码 URL
var loginSuccess bool       // 用于标记是否登录成功
var loginMutex sync.Mutex   // 用于保护 loginSuccess 变量
var botInitialized bool     // 用于标记 bot 是否初始化完成
var botInitMutex sync.Mutex // 用于保护 botInitialized 变量

func main() {
	// 检查 /app/static/index.html 文件是否存在
	if _, err := os.Stat("/app/static/index.html"); os.IsNotExist(err) {
		log.Fatalf("文件 /app/static/index.html 不存在")
	} else {
		log.Printf("文件 /app/static/index.html 存在")
	}

	// 设置日志输出
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 从环境变量加载配置
	loadConfigFromEnv()

	// 初始化 bot 和二维码
	go initBotAndQRCode()

	// 启动HTTP服务器
	go startHTTPServer()

	// 阻塞主程序
	select {}
}

func initBotAndQRCode() {
	// 创建一个新的机器人实例
	bot = openwechat.DefaultBot(openwechat.Desktop) // 初始化全局 bot 变量

	// 注册消息处理函数
	bot.MessageHandler = func(msg *openwechat.Message) {
		handleMessage(bot, msg)
	}

	// 注册登录事件
	bot.UUIDCallback = func(uuid string) {
		log.Printf("访问下面网址扫描二维码登录: https://login.weixin.qq.com/qrcode/%s", uuid)
		qrCodeUUID = uuid // 保存 UUID
		qrCodeUrl = fmt.Sprintf("https://login.weixin.qq.com/qrcode/%s", qrCodeUUID)
	}

	// 注册登录成功事件
	bot.LoginCallBack = func(body openwechat.CheckLoginResponse) {
		loginMutex.Lock()
		loginSuccess = true
		qrCodeUrl = "" // 清除二维码URL
		loginMutex.Unlock()
		log.Println("登录成功")
	}

	// 注册登出事件
	bot.LogoutCallBack = func(bot *openwechat.Bot) {
		loginMutex.Lock()
		loginSuccess = false
		loginMutex.Unlock()
		log.Println("已登出")
		// 重新初始化bot
		go initBotAndQRCode()
	}

	// 登录
	err := bot.Login()
	if err != nil {
		log.Printf("登录失败: %v", err)
		loginMutex.Lock()
		loginSuccess = false
		loginMutex.Unlock()
		return
	}

	// 获取登陆的用户
	self, err := bot.GetCurrentUser()
	if err != nil {
		log.Printf("获取当前用户失败: %v", err)
		return
	}
	log.Printf("登录成功: %s", self.NickName)

	// 初始化群组列表
	allGroups = make(map[string]bool)
	updateGroupList(bot, self)

	// 启动定时任务，每隔一段时间更新群组列表
	go func() {
		ticker := time.NewTicker(5 * time.Minute) // 每5分钟更新一次
		defer ticker.Stop()
		for range ticker.C {
			updateGroupList(bot, self)
		}
	}()

	botInitMutex.Lock()
	botInitialized = true
	botInitMutex.Unlock()
}

func handleMessage(bot *openwechat.Bot, msg *openwechat.Message) {
	if msg.IsSendBySelf() {
		return
	}

	var sender string
	var content string
	var groupName string

	if msg.IsSendByFriend() {
		friendSender, err := msg.Sender()
		if err != nil {
			log.Printf("获取发送者信息失败: %v", err)
			return
		}
		sender = friendSender.RemarkName
		if sender == "" {
			sender = friendSender.NickName
		}
	} else if msg.IsSendByGroup() {
		group, err := msg.Sender()
		if err != nil {
			log.Printf("获取群聊信息失败: %v", err)
			return
		}
		groupName = group.NickName // 获取群名

		groupSender, err := msg.SenderInGroup()
		if err != nil {
			log.Printf("获取群聊发送者信息失败: %v", err)
			return
		}
		// 优先使用群成员的备注名
		sender = groupSender.RemarkName
		if sender == "" {
			sender = groupSender.NickName
		}
	} else {
		log.Println("未知的消息发送者类型,视为公众号消息,屏蔽")
		return
	}

	switch {
	case msg.IsText():
		content = msg.Content
	case msg.IsPicture():
		content = "[图片]"
	case msg.IsVoice():
		content = "[语音]"
	case msg.IsVideo():
		content = "[视频]"
	case msg.IsEmoticon():
		content = "[动画表情]"
	default:
		content = "[未知类型消息]"
	}

	log.Printf("%s: %s", sender, content)

	// 判断是否发送邮件
	shouldSendEmail := false
	if msg.IsSendByGroup() {
		// 检查群组是否在通讯录中
		_, ok := allGroups[groupName]
		if ok {
			// 如果群组在通讯录中，所有消息都发送邮件
			shouldSendEmail = true
		} else {
			// 如果群组不在通讯录中，只在@所有人的情况下发送邮件
			if msg.IsText() && strings.Contains(msg.Content, "@所有人") {
				shouldSendEmail = true
			}
		}
	} else if !msg.IsSendByGroup() {
		// 如果不是群消息，直接发送邮件
		shouldSendEmail = true
	}

	if shouldSendEmail && content != "[未知类型消息]" {
		for i := 0; i < 3; i++ { // 重试3次
			if err := mail.SendEmail(sender, content); err != nil {
				log.Printf("发送邮件失败 (尝试 %d/3): %v", i+1, err)
				time.Sleep(time.Second * 2) // 等待2秒后重试
			} else {
				log.Printf("邮件发送成功: %s - %s", sender, content)
				return
			}
		}
		log.Printf("发送邮件失败，已达到最大重试次数")
	}
}

// 从环境变量加载配置
func loadConfigFromEnv() {
	blockedGroupsJSON := os.Getenv("BLOCKED_GROUPS")
	if blockedGroupsJSON == "" {
		config.BlockedGroups = []string{}
		return
	}

	err := json.Unmarshal([]byte(blockedGroupsJSON), &config.BlockedGroups)
	if err != nil {
		log.Fatalf("解析环境变量 BLOCKED_GROUPS 失败: %v", err)
	}
}

// 保存配置到环境变量
func saveConfigToEnv() {
	blockedGroupsJSON, err := json.Marshal(config.BlockedGroups)
	if err != nil {
		log.Fatalf("序列化配置失败: %v", err)
	}

	os.Setenv("BLOCKED_GROUPS", string(blockedGroupsJSON))
	log.Printf("已将配置保存到环境变量 BLOCKED_GROUPS: %s", string(blockedGroupsJSON))
}

func startHTTPServer() {
	var self *openwechat.Self
	// 获取当前用户所在的群组列表的 API 接口
	http.HandleFunc("/groups", func(w http.ResponseWriter, r *http.Request) {
		if self == nil {
			log.Printf("self 对象为空")
			http.Error(w, "获取群组列表失败", http.StatusInternalServerError)
			return
		}

		// 获取当前用户所在的群组列表, 强制更新群组列表
		// 等待一段时间，以便让微信服务器有时间同步群组信息
		time.Sleep(2 * time.Second)
		groups, err := self.Groups(true)
		if err != nil {
			log.Printf("获取群组列表失败: %v", err)
			http.Error(w, "获取群组列表失败", http.StatusInternalServerError)
			return
		}

		log.Printf("获取到的群组列表: %v", groups)

		groupList := make([]map[string]string, 0)
		for _, group := range groups {
			log.Printf("群组: %v", group)
			groupList = append(groupList, map[string]string{
				"name": group.NickName,
				"id":   group.NickName,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(groupList)
	})

	// 获取当前可以接收消息的群组列表
	http.HandleFunc("/active-groups", func(w http.ResponseWriter, r *http.Request) {
		activeGroups := make([]string, 0)
		for groupName := range allGroups {
			activeGroups = append(activeGroups, groupName)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(activeGroups)
	})

	// 获取登录状态和二维码
	http.HandleFunc("/login-status", func(w http.ResponseWriter, r *http.Request) {
		loginMutex.Lock()
		defer loginMutex.Unlock()
		botInitMutex.Lock()
		defer botInitMutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")

		if !botInitialized {
			if qrCodeUrl != "" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"isLogged":  false,
					"qrCodeUrl": qrCodeUrl,
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"isLogged":  false,
				"qrCodeUrl": "",
				"error":     "Bot 正在初始化，请稍后重试",
			})
			return
		}

		if bot == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"isLogged":  false,
				"qrCodeUrl": "",
				"error":     "Bot 未初始化",
			})
			return
		}

		// 检查bot是否存活
		if !bot.Alive() {
			loginSuccess = false
			json.NewEncoder(w).Encode(map[string]interface{}{
				"isLogged":  false,
				"qrCodeUrl": qrCodeUrl,
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"isLogged":  loginSuccess,
			"qrCodeUrl": qrCodeUrl,
		})
	})

	// 获取当前的配置信息
	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)
	})

	// 首页
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 使用绝对路径
		http.ServeFile(w, r, "/app/static/index.html")
		// 禁用缓存
		w.Header().Set("Cache-Control", "no-cache")
	})

	// 验证密码接口
	http.HandleFunc("/verify-password", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var data struct {
			Password string `json:"password"`
		}

		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		pagePassword := os.Getenv("PAGE_PASSWORD")
		if pagePassword == "" {
			http.Error(w, "PAGE_PASSWORD not set", http.StatusInternalServerError)
			return
		}

		if data.Password != pagePassword {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "密码错误",
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "验证成功",
		})
	})

	// 保存配置接口,添加密码验证
	http.HandleFunc("/save-config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			// 验证密码
			password := os.Getenv("PAGE_PASSWORD")
			if password == "" {
				log.Println("环境变量 PAGE_PASSWORD 未设置")
				http.Error(w, "服务器错误", http.StatusInternalServerError)
				return
			}
			inputPassword := r.URL.Query().Get("password")
			if inputPassword != password {
				log.Printf("密码错误")
				http.Error(w, "{\"error\": \"密码错误\"}", http.StatusUnauthorized)
				return
			}

			var newConfig Config
			err := json.NewDecoder(r.Body).Decode(&newConfig)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			config.BlockedGroups = newConfig.BlockedGroups
			saveConfigToEnv()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
		} else {
			http.Error(w, "无效的请求方法", http.StatusMethodNotAllowed)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("启动HTTP服务器在端口 %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("HTTP服务器启动失败:", err)
	}
}

func updateGroupList(bot *openwechat.Bot, self *openwechat.Self) {
	// 获取当前用户所在的群组列表, 强制更新群组列表
	// 等待一段时间，以便让微信服务器有时间同步群组信息
	time.Sleep(2 * time.Second)
	groups, err := self.Groups(true)
	if err != nil {
		log.Printf("获取群组列表失败: %v", err)
		return
	}

	allGroups = make(map[string]bool) // 清空之前的群组列表
	for _, group := range groups {
		allGroups[group.NickName] = true
	}
	log.Printf("已更新群组列表: %v", allGroups)
}
