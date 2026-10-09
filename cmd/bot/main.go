package main

import (
	"context"
	"example.com/whatfordinner/plugins/dinner"
	"flag"
	_ "github.com/RandomLemon/kei/adapters/mock"
	"github.com/RandomLemon/kei/pkg/bot"
	"github.com/RandomLemon/kei/pkg/kei"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	config := flag.String("config", "config.yaml", "宿主 YAML 配置路径")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := kei.Run(ctx, kei.Options{ConfigFile: *config, Plugins: []bot.Plugin{&dinner.Plugin{}}}); err != nil {
		log.Fatal(err)
	}
}
