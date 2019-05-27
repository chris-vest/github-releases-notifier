package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/joho/godotenv"
	"github.com/shurcooL/githubql"
	"golang.org/x/oauth2"
)

// Config of env and args
type Config struct {
	File         string        `arg:"-f"`
	GithubToken  string        `arg:"env:GITHUB_TOKEN"`
	Interval     time.Duration `arg:"env:INTERVAL"`
	LogLevel     string        `arg:"env:LOG_LEVEL"`
	Repositories []string      `arg:"-r,separate"`
	SlackHook    string        `arg:"env:SLACK_HOOK"`
}

// Token returns an oauth2 token or an error.
func (c Config) Token() *oauth2.Token {
	return &oauth2.Token{AccessToken: c.GithubToken}
}

func main() {
	_ = godotenv.Load()

	c := Config{
		File:     "output",
		Interval: time.Hour,
		LogLevel: "info",
	}
	arg.MustParse(&c)

	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stdout))
	logger = log.With(logger,
		"ts", log.DefaultTimestampUTC,
		"caller", log.Caller(5),
	)

	level.SetKey("severity")
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		logger = level.NewFilter(logger, level.AllowDebug())
	case "warn":
		logger = level.NewFilter(logger, level.AllowWarn())
	case "error":
		logger = level.NewFilter(logger, level.AllowError())
	default:
		logger = level.NewFilter(logger, level.AllowInfo())
	}

	tokenSource := oauth2.StaticTokenSource(c.Token())
	client := oauth2.NewClient(context.Background(), tokenSource)
	checker := &Checker{
		logger: logger,
		client: githubql.NewClient(client),
	}

	if _, err := os.Stat(c.File); err == nil {
		level.Info(logger).Log("msg", "found repository configuration file")
		lines, err := readFile(c.File)
		if err != nil {
			level.Error(logger).Log("%s", err)
		}
		for i, line := range lines {
			readFileMsg := fmt.Sprintf("reading repository configuration file: line %v of total %v", i+1, len(lines))
			level.Info(logger).Log("msg", readFileMsg)
			c.Repositories = append(c.Repositories, line)
		}
	} else if os.IsNotExist(err) {
		level.Warn(logger).Log("msg", "no configuration file exists, continuing and only using flagged arguments")
		level.Warn(logger).Log("err", err)
	}

	releases := make(chan Repository)
	go checker.Run(c.Interval, c.Repositories, releases)

	if c.SlackHook == "" {
		level.Error(logger).Log("err", "missing Slack Webhook URL; cannot create Slack notifications")
	}
	slack := SlackSender{Hook: c.SlackHook}

	level.Info(logger).Log("msg", "waiting for new releases")
	for repository := range releases {
		if err := slack.Send(repository); err != nil {
			level.Warn(logger).Log(
				"msg", "failed to send release to messenger",
				"err", err,
			)
			continue
		}
	}
}
