// mobilework-proxy（Go 版）
//
// 把本机已登录的 MobileWork 桌面端自带的「本地签名代理」包装成标准 OpenAI 兼容 API，
// 并可选地把模型一键安装进 DeepSeek Harness 的 profile 配置。
//
// 双击即用：探测模型 → 倒计时等你选 → 写进 cordis.patch.yml → 继续把代理跑着。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mobilework-proxy/internal/dsh"
	"mobilework-proxy/internal/mw"
	"mobilework-proxy/internal/proxy"
	"mobilework-proxy/internal/ui"
)

const (
	// DefaultPort 与 DSH 里已配的 baseURL http://localhost:8989/v1 保持一致。
	DefaultPort = 8989
	// DefaultCountdown 是选择界面的倒计时秒数。
	DefaultCountdown = 10
	// DefaultRouteProviderID 是写进 cordis.patch.yml 的路由名。
	DefaultRouteProviderID = "mobilework"
)

// version 是版本号，由构建时注入：
//
//	go build -ldflags "-X main.version=v0.1.0"
//
// 未注入时显示 dev。
var version = "dev"

func main() {
	var (
		host      = flag.String("host", "127.0.0.1", "监听地址")
		port      = flag.Int("port", DefaultPort, "监听端口")
		apiKey    = flag.String("api-key", os.Getenv("MW_OPENAI_KEY"), "可选：启用后客户端需带同样的 key")
		reload    = flag.Int("reload-interval", 30, "重新读取 providers.jsonc 的间隔秒数")
		countdown = flag.Int("countdown", DefaultCountdown, "选择界面的倒计时秒数（0=不等待，直接装默认项）")
		yes       = flag.Bool("yes", false, "跳过交互，直接安装默认项")
		installTo = flag.String("install", "", "安装到指定 profile（desktop/web/... 或 all）；给定则跳过 profile 选择")
		noInstall = flag.Bool("no-install", false, "只起代理，不改动 DSH 配置")
		showVer   = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Printf("mobilework-proxy %s\n", version)
		return
	}

	log.SetFlags(log.Ltime)
	defer ui.RestoreConsole()

	banner()

	// ---------- 1. 探测 MobileWork ----------
	entries, err := mw.Index()
	if err != nil {
		fatal("读取 MobileWork 模型清单失败：%v\n"+
			"请确认已安装并登录 MobileWork 桌面端（模型清单在 %s）。", err, mw.ProvidersFilePath())
	}
	if len(entries) == 0 {
		fatal("MobileWork 模型清单为空：%s", mw.ProvidersFilePath())
	}
	upstream, upErr := mw.UpstreamBase()
	if upErr != nil {
		log.Printf("[warn] 暂时拿不到内部签名代理地址: %v", upErr)
		log.Printf("[warn] 代理会照常启动，等 MobileWork 起来后自动可用")
	} else {
		log.Printf("[info] MobileWork 内部签名代理: %s", upstream)
	}

	// ---------- 2. 选择要安装的模型 ----------
	proxyPort := *port
	installedAny := false

	if *noInstall {
		log.Printf("[info] 已指定 --no-install，跳过 DSH 配置写入")
	} else {
		portInUseWarning(proxyPort)

		chosen, cancelled := chooseModels(entries, *countdown, *yes)
		if cancelled {
			log.Printf("[info] 已取消安装，代理仍会启动")
		} else {
			targets, err := resolveProfiles(*installTo)
			if err != nil {
				log.Printf("[warn] 定位 DSH profile 失败: %v", err)
			} else {
				for _, p := range targets {
					if installOne(p, chosen, proxyPort) {
						installedAny = true
					}
				}
			}
		}
	}

	if installedAny {
		fmt.Println()
		log.Printf("[info] 安装完成。DeepSeek Harness 中即可选用这些模型。")
	}
	fmt.Println()

	// ---------- 3. 常驻代理 ----------
	s := proxy.New(proxy.Options{
		Host:           *host,
		Port:           proxyPort,
		APIKey:         *apiKey,
		ReloadInterval: time.Duration(*reload) * time.Second,
	})
	if err := s.Run(); err != nil {
		fatal("代理启动失败：%v", err)
	}
}

// chooseModels 让用户挑要安装的模型；cancelled 表示用户主动放弃。
func chooseModels(entries []mw.ModelEntry, countdown int, yes bool) (chosen []mw.ModelEntry, cancelled bool) {
	if yes || countdown == 0 {
		log.Printf("[info] 非交互模式，采用默认项: %s", entries[0].Model)
		return entries[:1], false
	}

	// 模型名与说明分列对齐，配合界面自己画的 [编号]，一眼能看清
	maxName := 0
	for _, e := range entries {
		if n := len(e.Model); n > maxName {
			maxName = n
		}
	}
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		items = append(items, fmt.Sprintf("%-*s   %s", maxName, e.Model, describe(e)))
	}

	choice, err := ui.Choose(ui.Options{
		Title:    fmt.Sprintf("检测到 %d 个可用模型，请选择要安装到 DeepSeek Harness 的模型：", len(entries)),
		Items:    items,
		Timeout:  time.Duration(countdown) * time.Second,
		Default:  0,
		AllowAll: true,
	})
	switch {
	case err == ui.ErrQuit:
		return nil, true
	case err != nil:
		log.Printf("[warn] 交互失败（%v），采用默认项", err)
		return entries[:1], false
	case choice.Index == -1:
		return entries, false
	default:
		return entries[choice.Index : choice.Index+1], false
	}
}

// installOne 把选中的模型写进一个 profile；返回是否成功改动。
func installOne(p dsh.Profile, chosen []mw.ModelEntry, proxyPort int) bool {
	if !p.HasPatch {
		log.Printf("[warn] %s 没有 cordis.patch.yml，跳过", p.Name)
		return false
	}
	route := buildRoute(chosen, proxyPort)

	// 幂等预检：读出已装的路由，比对
	if cur, err := dsh.ReadRoute(p.Patch, route.ProviderID); err == nil && cur != nil {
		if dsh.RoutesEqual(cur, &route) {
			log.Printf("[info] %s：配置已是目标状态，无需改动", p.Name)
			return false
		}
	}

	res, err := dsh.Install(p.Patch, route)
	if err != nil {
		log.Printf("[error] %s 写入失败: %v", p.Name, err)
		return false
	}
	if res.Changed {
		log.Printf("[ok] %s：%s", p.Name, res.Summary)
		log.Printf("       备份: %s", res.BackupPath)
	} else {
		log.Printf("[info] %s：%s", p.Name, res.Summary)
	}
	return res.Changed
}

// buildRoute 把探测到的模型入口转换成要写入配置的路由。
func buildRoute(chosen []mw.ModelEntry, proxyPort int) dsh.Route {
	base := fmt.Sprintf("http://localhost:%d/v1", proxyPort)
	route := dsh.Route{
		ProviderID: DefaultRouteProviderID,
		APIKeyEnv:  "MOBILEWORK_API_KEY",
		API:        "openai-completions",
		BaseURL:    base,
	}
	for _, e := range chosen {
		input := e.Modalities
		if len(input) == 0 {
			input = []string{"text"}
		}
		route.Models = append(route.Models, dsh.RouteModel{
			ID:            e.Model,
			Name:          e.Label,
			ContextWindow: e.Context,
			MaxTokens:     e.Output,
			Input:         input,
		})
	}
	return route
}

// resolveProfiles 决定装到哪些 profile。
func resolveProfiles(installTo string) ([]dsh.Profile, error) {
	all, err := dsh.Locate()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("没有找到任何 profile")
	}

	// --install 指定
	if s := strings.TrimSpace(installTo); s != "" {
		if strings.EqualFold(s, "all") {
			return all, nil
		}
		for _, p := range all {
			if strings.EqualFold(p.Name, s) {
				return []dsh.Profile{p}, nil
			}
		}
		return nil, fmt.Errorf("没有名为 %q 的 profile", s)
	}

	// 动态选择：列出本机实际存在的 profile
	items := make([]string, 0, len(all)+1)
	for _, p := range all {
		tag := ""
		if p.Active {
			tag = "  (当前)"
		}
		items = append(items, p.Name+tag)
	}
	items = append(items, "全部")

	choice, err := ui.Choose(ui.Options{
		Title:   "要把模型安装到哪个 DSH profile？",
		Items:   items,
		Footer:  "（回车=默认项；Q=跳过安装）",
		Timeout: 30 * time.Second,
		Default: 0,
	})
	if err == ui.ErrQuit {
		return nil, fmt.Errorf("用户跳过")
	}
	if err != nil {
		return []dsh.Profile{all[0]}, nil
	}
	if choice.Index == -1 || choice.Index == len(all) {
		return all, nil
	}
	return []dsh.Profile{all[choice.Index]}, nil
}

func describe(e mw.ModelEntry) string {
	var parts []string
	if e.Context > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", e.Context))
	}
	if e.Output > 0 {
		parts = append(parts, fmt.Sprintf("out %d", e.Output))
	}
	if len(e.Modalities) > 0 {
		parts = append(parts, strings.Join(e.Modalities, "+"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// portInUseWarning 检查端口是否已被占用（通常是旧的 Python 版）。
func portInUseWarning(port int) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		ln.Close()
		return
	}
	fmt.Println()
	log.Printf("[warn] 端口 %d 已被占用（多半是旧的 openai_proxy.py 还在跑）。", port)
	log.Printf("[warn] 请先关掉它再运行本程序，或用 --port 换一个端口，")
	log.Printf("[warn] 换端口后记得同步改 DSH 配置里的 baseURL。")
	fmt.Println()
}

func banner() {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────────────┐")
	fmt.Println("  │   MobileWork → OpenAI 兼容代理  +  DSH 模型安装器     │")
	fmt.Println("  │   Go 版 · 零依赖 · 双击即用                          │")
	fmt.Println("  └──────────────────────────────────────────────────────┘")
	fmt.Printf("   版本 %s\n", version)
	fmt.Println()
}

func usage() {
	self := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `%s —— MobileWork OpenAI 兼容代理 + DSH 模型安装器

用法:
  %s [选项]

选项:
  --host string             监听地址（默认 127.0.0.1）
  --port int                监听端口（默认 %d）
  --api-key string          可选：启用后客户端需带同样的 key
  --reload-interval int     重读 providers.jsonc 的间隔秒数（默认 30）
  --countdown int           选择界面倒计时秒数（默认 %d；0=不等待）
  --yes                     跳过交互，直接安装默认项
  --install string          安装到指定 profile（desktop/web/... 或 all）
  --no-install              只起代理，不改动 DSH 配置
  --version                 打印版本后退出

说明:
  本程序复用 MobileWork 桌面端已登录的会话，自身不持有任何凭据。
  请先启动并登录 MobileWork 桌面端。
`, self, self, DefaultPort, DefaultCountdown)
}

func fatal(format string, a ...any) {
	ui.RestoreConsole()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  ✗ "+format+"\n\n", a...)
	os.Exit(1)
}
