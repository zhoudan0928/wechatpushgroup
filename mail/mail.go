package mail

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

var (
	from       mail.Address
	to         mail.Address
	smtpServer string
	smtpPort   string
	username   string
	password   string
)

func init() {
	// 尝试在不同位置加载 .env 文件
	envPaths := []string{".env", "../.env", "../../.env", "/app/.env"}
	envLoaded := false

	for _, path := range envPaths {
		absPath, _ := filepath.Abs(path)
		if err := godotenv.Load(absPath); err == nil {
			log.Printf("成功加载 .env 文件: %s", absPath)
			envLoaded = true
			break
		}
	}

	if !envLoaded {
		log.Println("警告: 无法加载 .env 文件，将使用环境变量")
	}

	from = mail.Address{
		Name:    getEnv("FROM_NAME", "发件人"),
		Address: getEnv("FROM_ADDRESS", ""),
	}
	to = mail.Address{
		Name:    getEnv("TO_NAME", "收件人"),
		Address: getEnv("TO_ADDRESS", ""),
	}
	smtpServer = getEnv("SMTP_SERVER", "")
	smtpPort = getEnv("SMTP_PORT", "465")
	username = getEnv("FROM_ADDRESS", "") // 使用FROM_ADDRESS作为username
	password = getEnv("PASSWORD", "")

	// 添加调试日志
	log.Printf("调试: 从环境变量读取的值:")
	log.Printf("FROM_ADDRESS: %s", from.Address)
	log.Printf("TO_ADDRESS: %s", to.Address)
	log.Printf("SMTP_SERVER: %s", smtpServer)
	log.Printf("SMTP_PORT: %s", smtpPort)
	log.Printf("USERNAME: %s", username)
	log.Printf("PASSWORD: %s", password[:4]+"****") // 只显示密码的前四个字符

	if from.Address == "" || to.Address == "" || smtpServer == "" || username == "" || password == "" {
		log.Println("警告: 一些必要的环境变量未设置")
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func SendEmail(name string, content string) error {
	// 添加调试日志
	log.Printf("调试: 发送邮件时使用的值:")
	log.Printf("SMTP_SERVER: %s", smtpServer)
	log.Printf("SMTP_PORT: %s", smtpPort)
	log.Printf("USERNAME: %s", username)
	log.Printf("PASSWORD: %s", password[:4]+"****") // 只显示密码的前四个字符

	// 连接到服务器
	addr := fmt.Sprintf("%s:%s", smtpServer, smtpPort)
	log.Printf("尝试连接到SMTP服务器: %s", addr)

	// 建立SSL连接
	tlsConfig := &tls.Config{ServerName: smtpServer}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("无法建立SSL连接: %v", err)
	}
	defer conn.Close()

	// 创建SMTP客户端
	client, err := smtp.NewClient(conn, smtpServer)
	if err != nil {
		return fmt.Errorf("创建SMTP客户端失败: %v", err)
	}
	defer client.Close()

	// 使用PlainAuth进行认证
	auth := smtp.PlainAuth("", username, password, smtpServer)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %v", err)
	}

	// 设置发件人和收件人
	if err = client.Mail(from.Address); err != nil {
		return fmt.Errorf("设置发件人失败: %v", err)
	}
	if err = client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("设置收件人失败: %v", err)
	}

	// 发送邮件正文
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("创建邮件数据写入器失败: %v", err)
	}
	defer w.Close()

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		from.String(), to.String(), name, content)

	_, err = w.Write([]byte(msg))
	if err != nil {
		return fmt.Errorf("写入邮件内容失败: %v", err)
	}

	log.Println("邮件发送成功")
	return nil
}
