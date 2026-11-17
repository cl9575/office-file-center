package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"

	"lanfile/internal/api"
	"lanfile/internal/audit"
	"lanfile/internal/auth"
	"lanfile/internal/backup"
	"lanfile/internal/certsetup"
	"lanfile/internal/config"
	"lanfile/internal/db"
	"lanfile/internal/files"
	"lanfile/internal/logging"
	"lanfile/internal/service"
	"lanfile/internal/settings"
	"lanfile/internal/storage"
)

var version = "0.1.0"

func main() {
	dataDir := flag.String("data", "data", "数据目录，必须使用本地 NTFS 磁盘")
	addr := flag.String("addr", ":9443", "监听地址，例如 :9443")
	tlsCert := flag.String("tls-cert", "", "自定义 HTTPS 证书 PEM 路径")
	tlsKey := flag.String("tls-key", "", "自定义 HTTPS 私钥 PEM 路径")
	httpMode := flag.Bool("http", false, "关闭 HTTPS，仅用于本机测试")
	hosts := flag.String("hosts", "", "证书附加主机名或 IP，逗号分隔")
	restoreDir := flag.String("restore", "", "恢复模式：完整备份目录")
	restoreTo := flag.String("restore-to", "", "恢复模式：空的恢复目标目录")
	showVersion := flag.Bool("version", false, "显示版本号")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *restoreDir != "" {
		if *restoreTo == "" {
			fmt.Fprintln(os.Stderr, "恢复模式必须同时提供 -restore-to")
			os.Exit(2)
		}
		if err := backup.Restore(*restoreDir, *restoreTo); err != nil {
			fmt.Fprintf(os.Stderr, "恢复失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("恢复完成：%s\n", *restoreTo)
		return
	}

	cfg := &config.Config{
		DataDir: *dataDir, Addr: *addr, TLSCert: *tlsCert, TLSKey: *tlsKey,
		HTTP: *httpMode, Hosts: splitList(*hosts), Version: version,
	}
	if err := cfg.Normalize(); err != nil {
		log.Fatalf("配置错误：%v", err)
	}
	closeLog, err := logging.Setup(cfg.LogDir())
	if err != nil {
		log.Fatalf("初始化日志失败：%v", err)
	}
	defer closeLog.Close()

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("打开数据库失败：%v", err)
	}
	defer database.Close()

	store := storage.New(cfg.ObjectsDir(), cfg.TempDir())
	auditSvc := &audit.Service{DB: database}
	settingsSvc := &settings.Service{DB: database}
	authSvc := auth.New(database)
	filesSvc := files.New(database, store, auditSvc, settingsSvc)
	backupSvc := backup.New(database, cfg.DBPath(), cfg.DataDir, store, settingsSvc, auditSvc)

	certInfo := certsetup.Info{}
	certPath, keyPath := cfg.TLSCert, cfg.TLSKey
	tlsEnabled := !cfg.HTTP
	if tlsEnabled {
		if certPath == "" {
			certPath, keyPath, certInfo, err = certsetup.Ensure(cfg.CertDir(), cfg.Hosts)
			if err != nil {
				log.Fatalf("生成 HTTPS 证书失败：%v", err)
			}
		} else {
			certInfo, err = certsetup.Inspect(certPath)
			if err != nil {
				log.Fatalf("读取 HTTPS 证书失败：%v", err)
			}
		}
	}

	server := api.New(cfg, database, authSvc, filesSvc, auditSvc, settingsSvc, backupSvc,
		certInfo, tlsEnabled, certPath, keyPath, version)

	log.Printf("数据目录：%s", cfg.DataDir)
	log.Printf("版本：%s", version)
	if tlsEnabled {
		log.Printf("证书指纹：%s", certInfo.Fingerprint)
		for _, host := range accessHosts(certInfo) {
			log.Printf("访问地址：https://%s%s", bracketHost(host), portSuffix(cfg.Addr, "9443"))
		}
	} else {
		log.Printf("访问地址：http://%s%s", "localhost", portSuffix(cfg.Addr, "8080"))
	}

	run := func(ctx context.Context) error { return server.Run(ctx) }
	isService, err := service.IsWindowsService()
	if err != nil {
		log.Printf("检测 Windows 服务状态失败，按控制台模式运行：%v", err)
	}
	if isService {
		if err := service.Run("LanFilePlatform", run); err != nil {
			log.Fatalf("Windows 服务运行失败：%v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatalf("服务运行失败：%v", err)
	}
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func portSuffix(addr, fallback string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = fallback
	}
	if port == "443" || port == "80" {
		return ""
	}
	return ":" + port
}

func bracketHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func accessHosts(info certsetup.Info) []string {
	var out []string
	out = append(out, info.DNSNames...)
	out = append(out, info.IPAddresses...)
	if len(out) == 0 {
		out = append(out, "localhost")
	}
	return out
}
