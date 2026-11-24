package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"lanchat/internal/chat"
	"lanchat/internal/voice"
	"lanchat/web"
)

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
func main() {
	addr := env("LISTEN_ADDR", ":18086")
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(addr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	data := env("DATA_DIR", "data")
	if err := os.MkdirAll(filepath.Join(data, "audio"), 0700); err != nil {
		log.Fatal(err)
	}
	store, err := chat.OpenStore(filepath.Join(data, "chat.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	room, err := voice.NewRoom(voice.Config{HostIP: env("VOICE_HOST_IP", ""), UDPAddr: env("VOICE_UDP_ADDR", ":18087"), TCPAddr: env("VOICE_TCP_ADDR", ":18085")})
	if err != nil {
		log.Fatal(err)
	}
	defer room.Close()
	app := chat.NewServer(store, filepath.Join(data, "audio"), room, web.Files)
	defer app.Close()
	server := &http.Server{Addr: addr, Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	dev := env("DEV_HTTP", "0") == "1"
	if dev {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			log.Fatal("DEV_HTTP 仅允许绑定回环地址，例如 127.0.0.1:18086")
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		app.Close()
		room.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("LAN Chat listening on %s (HTTPS=%t), voice UDP=%s TCP=%s, data=%s", addr, !dev, env("VOICE_UDP_ADDR", ":18087"), env("VOICE_TCP_ADDR", ":18085"), data)
	if dev {
		err = server.ListenAndServe()
	} else {
		err = server.ListenAndServeTLS(env("TLS_CERT", "certs/server.crt"), env("TLS_KEY", "certs/server.key"))
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
func healthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	scheme := "https"
	transport := &http.Transport{}
	if env("DEV_HTTP", "0") == "1" {
		scheme = "http"
	} else {
		cert, err := os.ReadFile(env("TLS_CA", "certs/ca.crt"))
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cert) {
			return fmt.Errorf("invalid CA")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: transport}
	resp, err := client.Get(scheme + "://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}
