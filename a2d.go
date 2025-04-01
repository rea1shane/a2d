package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/blinkbean/dingtalk"
	"github.com/gin-gonic/gin"
	"github.com/rea1shane/gooooo/data"
	myHttp "github.com/rea1shane/gooooo/http"
	"github.com/rea1shane/gooooo/log"
	myTime "github.com/rea1shane/gooooo/time"
	"github.com/sirupsen/logrus"
)

// Notification Alertmanager 发送的告警通知
type Notification struct {
	Receiver string  `json:"receiver"`
	Status   string  `json:"status"`
	Alerts   []Alert `json:"alerts"`

	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`

	ExternalURL string `json:"externalURL"`
}

// Alert 告警实例
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

var (
	tmplPath, tmplName string
	logger             *logrus.Logger
)

func main() {
	// 解析命令行参数
	logLevel := flag.String("log-level", "info", "日志级别。可选值：debug, info, warn, error")
	addr := flag.String("addr", ":6001", "监听地址。格式: [host]:port")
	flag.StringVar(&tmplPath, "template", "./templates/base.tmpl", "模板文件路径。")
	flag.Parse()

	// 解析日志级别
	level, err := logrus.ParseLevel(*logLevel)
	if err != nil {
		logrus.Panicf("日志级别解析失败: %s", *logLevel)
	}

	// 解析模板文件名称
	split := strings.Split(tmplPath, "/")
	tmplName = split[len(split)-1]

	// 创建 logger
	logger = logrus.New()
	logger.SetLevel(level)
	formatter := log.NewFormatter()
	formatter.FieldsOrder = []string{"StatusCode", "Latency"}
	logger.SetFormatter(formatter)

	// 创建 Gin
	app := myHttp.NewHandler(logger, 0)

	app.GET("/", health)
	app.POST("/send", send)

	// 启动
	app.Run(*addr)
}

// health 健康检查
func health(c *gin.Context) {
	c.Writer.WriteString("ok")
}

// send 发送消息
func send(c *gin.Context) {
	// 参数检查
	tokens := c.QueryArray("token")
	if len(tokens) == 0 {
		c.Writer.WriteString("缺少 URL 参数 token")
		c.Writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// 读取 Alertmanager 消息
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		e := c.Error(err)
		e.Meta = "读取 Alertmanager 消息失败"
		c.Writer.WriteHeader(http.StatusBadRequest)
		return
	}
	logger.Debugf("Alertmanager request body: %s", string(body))

	// 解析 Alertmanager 消息
	var notification *Notification
	err = data.UnmarshalBytes(body, &notification, data.JsonFormat)
	if err != nil {
		e := c.Error(err)
		e.Meta = "解析 Alertmanager 消息失败"
		c.Writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// 填充模板
	var tfm = make(template.FuncMap)
	tfm["timeFormat"] = timeFormat
	tfm["timeDuration"] = timeDuration
	tfm["timeFromNow"] = timeFromNow
	tmpl := template.Must(template.New(tmplName).Funcs(tfm).ParseFiles(tmplPath))
	var content bytes.Buffer
	if err := tmpl.Execute(&content, notification); err != nil {
		e := c.Error(err)
		e.Meta = "填充模板失败"
		c.Writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 构建标题
	var (
		firingCount   = 0
		resolvedCount = 0
	)
	for _, alert := range notification.Alerts {
		if alert.Status == "firing" {
			firingCount++
		} else if alert.Status == "resolved" {
			resolvedCount++
		} else {
			logger.Warnf("未知的状态：%s", alert.Status)
		}
	}

	// DingTalk 客户端
	cli := dingtalk.InitDingTalk(tokens, "")
	mobiles := c.QueryArray("mobile")
	err = cli.SendMarkDownMessage(fmt.Sprintf("firing: %d, resolved: %d", firingCount, resolvedCount), content.String(), dingtalk.WithAtMobiles(mobiles))
	if err != nil {
		e := c.Error(err)
		e.Meta = "发起钉钉请求失败"
		c.Writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.Writer.WriteHeader(http.StatusOK)
}

// timeFormat 格式化时间
func timeFormat(t time.Time) string {
	return t.In(time.Local).Format("2006-01-02 15:04:05")
}

// timeDuration 计算结束时间距开始时间的时间差
func timeDuration(startTime, endTime time.Time) string {
	duration := endTime.Sub(startTime)
	return myTime.FormatDuration(duration)
}

// timeFromNow 计算当前时间距开始时间的时间差
func timeFromNow(startTime time.Time) string {
	return timeDuration(startTime, time.Now())
}
