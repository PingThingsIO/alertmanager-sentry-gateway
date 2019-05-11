package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/raven-go"
	"github.com/prometheus/alertmanager/notify"
	amt "github.com/prometheus/alertmanager/template"
	"github.com/spf13/cobra"
)

var (
	VERSION string = "latest"
	COMMIT  string = "HEAD"
)

const (
	defaultTemplate = "{{ .Labels.alertname }} - {{ .Labels.namespace }}/{{ .Labels.pod_name}}\n{{ .Annotations.message }}"
)

func main() {
	var cmd = &cobra.Command{
		Use:   "sentry-gateway",
		Short: "Sentry gateway for Alertmanager",
		RunE:  run,
	}

	cmd.Flags().StringP("dsn", "d", "", "Sentry DSN")
	cmd.Flags().StringP("template", "t", "", "Path of the template file of event message")
	cmd.Flags().StringP("addr", "a", "0.0.0.0:9096", "Address to listen on for WebHook")
	cmd.Flags().Bool("version", false, "Display version information and exit")

	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	v, err := cmd.Flags().GetBool("version")
	if err != nil {
		return err
	}

	if v {
		version()
		os.Exit(0)
	}

	dsn, err := cmd.Flags().GetString("dsn")
	if err != nil {
		return err
	}

	tmplPath, err := cmd.Flags().GetString("template")
	if err != nil {
		return err
	}

	addr, err := cmd.Flags().GetString("addr")
	if err != nil {
		return err
	}

	if dsn == "" {
		return errors.New("Sentry DSN required")
	}
	raven.SetDSN(dsn)

	tmpl := defaultTemplate
	if tmplPath != "" {
		file, err := ioutil.ReadFile(tmplPath)
		if err != nil {
			return err
		}

		tmpl = string(file)
	}

	t := template.New("").Option("missingkey=zero")
	t.Funcs(template.FuncMap(amt.DefaultFuncs))
	t, err = t.Parse(tmpl)
	if err != nil {
		return err
	}

	hookChan := make(chan *notify.WebhookMessage)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var wh notify.WebhookMessage

		decoder := json.NewDecoder(r.Body)
		defer r.Body.Close()

		err := decoder.Decode(&wh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid webhook: %s\n", err)
			return
		}

		hookChan <- &wh
	})

	s := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		err := s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Unable to start server: %s\n", err)
			os.Exit(1)
		}
	}()

	go worker(hookChan, t)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = s.Shutdown(ctx)
	if err != nil {
		return err
	}

	for len(hookChan) > 0 {
		time.Sleep(1)
	}
	close(hookChan)

	return nil
}

func worker(hookChan chan *notify.WebhookMessage, t *template.Template) {
	for wh := range hookChan {
		for _, alert := range wh.Alerts {
			var buf bytes.Buffer
			var msg string
			err := t.Execute(&buf, alert)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid template: %s\n", err)
				if alert.Labels["alertname"] == "" {
					msg = "fallback"
				} else {
					msg = alert.Labels["alertname"]
				}
			} else {
				msg = buf.String()
			}
			packet := &raven.Packet{
				Timestamp: raven.Timestamp(time.Now()),
				Message:   msg,
				Extra: map[string]interface{}{
					"firing_since": raven.Timestamp(alert.StartsAt),
					"firing_until": raven.Timestamp(alert.EndsAt)},
				Logger:      "alertmanager",
				Fingerprint: []string{alert.Labels["alertname"], alert.Labels["namespace"], alert.Labels["pod_name"]},
				ServerName:  fmt.Sprintf("%s/%s", alert.Labels["namespace"], alert.Labels["pod_name"]),
			}

			eventID, ch := raven.Capture(packet, alert.Labels)
			<-ch

			log.Printf("event_id:%s alert_name:%s\n", eventID, alert.Labels["alertname"])
		}
	}
}

func version() {
	fmt.Printf("Version: %s (%s)\n", VERSION, COMMIT)
}
