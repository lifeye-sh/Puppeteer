package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"net"
	"net/http"
	"next-terminal/backend/internal/nativeclient"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, nativeclient.Version)
		return nil
	}
	if len(os.Args) < 2 || (os.Args[1] != "assets" && os.Args[1] != "rdp" && os.Args[1] != "connect" && os.Args[1] != "forward" && os.Args[1] != "login" && os.Args[1] != "logout") {
		return fmt.Errorf("usage: nt-client assets|connect|forward|rdp|login|logout --server https://platform.example [--asset UUID] [--ca ca.pem]; login --remember [--allow-connect]; assets|connect|forward|rdp --use-login")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	server := flags.String("server", "", "platform HTTPS origin")
	ca := flags.String("ca", "", "additional trusted CA PEM")
	manual := flags.Bool("no-browser", false, "print login URL without opening the browser")
	deviceName := flags.String("device-name", "Puppeteer 客户端", "display name in personal login devices (not a hardware identity)")
	remember := flags.Bool("remember", false, "explicitly save a rotating refresh token in the system credential store (login only)")
	useLogin := flags.Bool("use-login", false, "use an existing system credential instead of browser login")
	allowConnect := flags.Bool("allow-connect", false, "include connection permission in saved login (login only)")
	asset := flags.String("asset", "", "authorized asset UUID")
	commandLine := flags.String("command", "", "explicit remote command; default opens an interactive shell")
	target := flags.String("target", "", "whitelisted destination host:port (forward only)")
	once := flags.Bool("once", false, "accept one local TCP connection then exit (forward only)")
	localPort := flags.Int("local-port", 0, "loopback listening port; 0 selects a free port")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	var invalidFlag string
	flags.Visit(func(f *flag.Flag) {
		if (f.Name == "remember" || f.Name == "allow-connect") && os.Args[1] != "login" {
			invalidFlag = f.Name
		}
		if f.Name == "use-login" && (os.Args[1] == "login" || os.Args[1] == "logout") {
			invalidFlag = f.Name
		}
		if (os.Args[1] == "login" || os.Args[1] == "logout") && (f.Name == "asset" || f.Name == "command") {
			invalidFlag = f.Name
		}
	})
	if invalidFlag != "" {
		return fmt.Errorf("--%s is not valid for %s", invalidFlag, os.Args[1])
	}
	if os.Args[1] == "login" && !*remember {
		return fmt.Errorf("login requires --remember; other commands default to temporary browser login")
	}
	var forwardOnly string
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "once" || f.Name == "target" || f.Name == "local-port" {
			forwardOnly = f.Name
		}
	})
	if forwardOnly != "" && os.Args[1] != "forward" {
		return fmt.Errorf("--%s requires forward", forwardOnly)
	}
	if os.Args[1] == "rdp" && runtime.GOOS != "windows" {
		return errors.New("rdp requires Windows mstsc")
	}
	if (os.Args[1] == "connect" || os.Args[1] == "forward" || os.Args[1] == "rdp") && *asset == "" {
		return fmt.Errorf("%s requires --asset UUID", os.Args[1])
	}
	if os.Args[1] == "forward" {
		h, p, e := net.SplitHostPort(*target)
		n, err := strconv.Atoi(p)
		if e != nil || err != nil || h == "" || n < 1 || n > 65535 || *localPort < 0 || *localPort > 65535 || *commandLine != "" {
			return fmt.Errorf("forward requires --target host:port and --local-port 0..65535, without --command")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if *ca != "" {
		pem, e := os.ReadFile(*ca)
		if e != nil {
			return e
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("invalid CA PEM")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	client, e := nativeclient.New(*server, nativeclient.LocalhostTransport(transport))
	if e != nil {
		return e
	}
	client.SetDeviceName(*deviceName)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	open := func(address string) error {
		fmt.Fprintln(os.Stderr, "请在浏览器登录并确认客户端授权：\n"+address)
		if !*manual {
			var command *exec.Cmd
			switch runtime.GOOS {
			case "windows":
				command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
			case "darwin":
				command = exec.Command("open", address)
			default:
				command = exec.Command("xdg-open", address)
			}
			if e := command.Start(); e == nil {
				go command.Wait()
			} else {
				fmt.Fprintln(os.Stderr, "无法自动打开浏览器，请手动打开上方地址。")
			}
		}
		return nil
	}
	var saved nativeclient.CredentialStore
	if *useLogin || os.Args[1] == "login" || os.Args[1] == "logout" {
		saved, e = client.SystemCredentialStore()
		if e != nil {
			return e
		}
	}
	if os.Args[1] == "login" {
		if e := client.RememberLogin(ctx, saved, open, *allowConnect); e != nil {
			return e
		}
		fmt.Fprintln(os.Stderr, "登录已保存到系统凭据管理器；使用 --use-login 复用，使用 logout 注销。")
		return nil
	}
	if os.Args[1] == "logout" {
		if e := client.ForgetLogin(ctx, saved); e != nil {
			return e
		}
		fmt.Fprintln(os.Stderr, "本机保存的登录已清除，远端授权已撤销（或本机原本无保存记录）。")
		return nil
	}
	var token nativeclient.Token
	if *useLogin {
		token, e = client.RememberedToken(ctx, saved, os.Args[1] != "assets")
	} else if os.Args[1] == "assets" {
		token, e = client.Login(ctx, open)
	} else {
		token, e = client.LoginForConnect(ctx, open)
	}
	if e != nil {
		return e
	}
	defer func() {
		if *useLogin {
			return
		}
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := client.Logout(ctx, token); e != nil {
			fmt.Fprintln(os.Stderr, "未能注销本次短期令牌，可在个人中心撤销应用授权。")
		}
	}()
	if os.Args[1] != "assets" {
		if e = client.PrepareAccess(ctx, token, func() (string, error) {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return "", fmt.Errorf("接入验证需要交互式终端，请在终端中重新运行命令")
			}
			fmt.Fprint(os.Stderr, "远程资产接入动态口令：")
			code, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			defer clear(code)
			return string(code), err
		}); e != nil {
			return e
		}
	}
	if os.Args[1] == "forward" {
		peer, e := client.ConnectForward(ctx, token, *asset)
		if e != nil {
			return e
		}
		defer peer.Close()
		listener, e := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(*localPort)))
		if e != nil {
			return e
		}
		defer listener.Close()
		fmt.Fprintln(os.Stderr, "HTTPS forwarding listening on", listener.Addr(), "to", *target, "; connect within 60 seconds; Ctrl+C to stop")
		if *once {
			return nativeclient.ServeForwardOnce(ctx, peer, listener, *target)
		}
		return nativeclient.ServeForward(ctx, peer, listener, *target)
	}
	if os.Args[1] == "rdp" {
		file, err := client.RDPFile(ctx, token, *asset)
		if err != nil {
			return err
		}
		defer clear(file)
		dir, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, "nt-record-view-*.rdp")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(file)
		closeErr := tmp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "mstsc.exe"), "/public", tmp.Name()).Run()
	}
	if os.Args[1] == "connect" {
		return runSSH(ctx, client, token, *asset, *commandLine)
	}
	assets, e := client.Assets(ctx, token)
	if e != nil {
		return e
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(assets)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		var remote *ssh.ExitError
		if errors.As(e, &remote) && remote.ExitStatus() > 0 && remote.ExitStatus() < 256 {
			os.Exit(remote.ExitStatus())
		}
		os.Exit(1)
	}
}
