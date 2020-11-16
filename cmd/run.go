package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"code.vegaprotocol.io/go-wallet/wallet"

	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
	"github.com/webview/webview"
	"go.uber.org/zap"
)

var (
	runArgs struct {
		consoleProxy bool
		consoleUI    bool
	}

	// runCmd represents the run command
	runCmd = &cobra.Command{
		Use:   "run",
		Short: "Start the vega wallet service",
		Long:  "Start a vega wallet service behind an http server",
		RunE:  runServiceRun,
	}
)

func init() {
	serviceCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVarP(&runArgs.consoleProxy, "console-proxy", "p", false, "Start the vega console proxy and open the console in the default browser")
	runCmd.Flags().BoolVarP(&runArgs.consoleUI, "console-ui", "u", false, "Start the vega console proxy and open the console in the native UI")
}

func runServiceRun(cmd *cobra.Command, args []string) error {
	cfg, err := wallet.LoadConfig(rootArgs.rootPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log, err := zap.NewProduction()
	if err != nil {
		return err
	}

	srv, err := wallet.NewService(log, cfg, rootArgs.rootPath)
	if err != nil {
		return err
	}
	go func() {
		defer cancel()
		err := srv.Start()
		if err != nil && err != http.ErrServerClosed {
			log.Error("error starting wallet http server", zap.Error(err))
		}
	}()

	var cproxy *consoleProxy

	if runArgs.consoleProxy || runArgs.consoleUI {
		cproxy = newConsoleProxy(log, cfg.Console.LocalPort, cfg.Console.URL)
		go func() {
			defer cancel()
			err := cproxy.Start()
			if err != nil && err != http.ErrServerClosed {
				log.Error("error starting console proxy server", zap.Error(err))
			}
		}()
	}

	var w webview.WebView
	if runArgs.consoleProxy {
		// then we open the console for the user straight at the right runServiceRun
		err := open.Run(cproxy.GetBrowserURL())
		if err != nil {
			log.Error("unable to open the console in the default browser",
				zap.Error(err))
		}

	} else if runArgs.consoleUI {
		w = webview.New(false)
		defer w.Destroy()
		w.SetTitle("Vega Console")
		w.SetSize(800, 600, webview.HintNone)
		w.Navigate(cproxy.GetBrowserURL())
	}

	go waitSig(ctx, cancel, log, w)

	if w != nil {
		w.Run()
		w.Destroy()
	}

	<-ctx.Done()

	err = srv.Stop()
	if err != nil {
		log.Error("error stopping wallet http server", zap.Error(err))
	} else {
		log.Info("wallet http server stopped with success")
	}

	if runArgs.consoleProxy {
		err = cproxy.Stop()
		if err != nil {
			log.Error("error stopping console proxy server", zap.Error(err))
		} else {
			log.Info("console proxy server stopped with success")
		}
	}

	return nil
}

// waitSig will wait for a sigterm or sigint interrupt.
func waitSig(ctx context.Context, cfunc func(), log *zap.Logger, w webview.WebView) {
	var gracefulStop = make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	signal.Notify(gracefulStop, syscall.SIGQUIT)

	select {
	case sig := <-gracefulStop:
		log.Info("Caught signal", zap.String("name", fmt.Sprintf("%+v", sig)))
		if w != nil {
			w.Terminate()
		}
		cfunc()
	case <-ctx.Done():
		// nothing to do
	}
}
