package controllers

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"pingoo/middleware"
	"pingoo/services"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type WebController struct {
}

func NewWebController() *WebController {
	return &WebController{}
}

func (wc *WebController) Index(c *gin.Context) {
	c.HTML(http.StatusOK, "index", OutputCommonSession(c, gin.H{
		"Title": "开源、简单、轻量、隐私友好的网站分析工具",
	}))
}

func (wc *WebController) Login(c *gin.Context) {
	c.HTML(http.StatusOK, "login", OutputCommonSession(c, gin.H{
		"Title": "登录",
	}))
}

func (wc *WebController) Register(c *gin.Context) {
	c.HTML(http.StatusOK, "register", OutputCommonSession(c, gin.H{
		"Title": "注册",
	}))
}

func (wc *WebController) Dashboard(c *gin.Context) {
	// 判断是否登录，未登录跳转登陆页面
	userInfo := middleware.GetCurrentUser(c)
	if userInfo == nil {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}
	c.HTML(http.StatusOK, "dashboard", OutputCommonSession(c, gin.H{
		"Title": "仪表盘",
	}))
}

func (wc *WebController) Websites(c *gin.Context) {
	// 判断是否登录，未登录跳转登陆页面
	userInfo := middleware.GetCurrentUser(c)
	if userInfo == nil {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}

	siteID := c.Param("id")

	// 默认事件为当天
	today := time.Now().Format("2006-01-02")
	// 从数据库中查询网站信息
	siteService := services.NewSiteService()
	// 将字符串类型的 siteID 转换为 uint64
	siteIDUint, err := strconv.ParseUint(siteID, 10, 64)
	if err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "无效的网站ID",
			"errorCode":    "ERROR",
			"errorTitle":   "无效的网站ID",
			"errorMessage": "检查网站ID是否正确或者返回首页",
		}))
		return
	}
	site, err := siteService.GetSiteByID(siteIDUint)
	if err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "网站不存在",
			"errorCode":    "ERROR",
			"errorTitle":   "网站不存在",
			"errorMessage": "检查网站ID是否正确或者返回首页",
		}))
		return
	}

	c.HTML(http.StatusOK, "websites", OutputCommonSession(c, gin.H{
		"Title": site.Name + " 网站详情",
		"Site":  site,
		"Today": today,
	}))
}

// Profile 用户中心页面
func (wc *WebController) Profile(c *gin.Context) {
	// 判断是否登录，未登录跳转登陆页面
	userInfo := middleware.GetCurrentUser(c)
	if userInfo == nil {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}
	c.HTML(http.StatusOK, "profile", OutputCommonSession(c, gin.H{
		"Title": "用户中心",
	}))
}

// Detail 通用详情页面
func (wc *WebController) Detail(c *gin.Context) {
	// 判断是否登录，未登录跳转登陆页面
	userInfo := middleware.GetCurrentUser(c)
	if userInfo == nil {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}

	siteID := c.Param("id")

	// 默认事件为当天
	today := time.Now().Format("2006-01-02")
	// 从数据库中查询网站信息
	siteService := services.NewSiteService()
	// 将字符串类型的 siteID 转换为 uint64
	siteIDUint, err := strconv.ParseUint(siteID, 10, 64)
	if err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "无效的网站ID",
			"errorCode":    "ERROR",
			"errorTitle":   "无效的网站ID",
			"errorMessage": "检查网站ID是否正确或者返回首页",
		}))
		return
	}
	site, err := siteService.GetSiteByID(siteIDUint)
	if err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "网站不存在",
			"errorCode":    "ERROR",
			"errorTitle":   "网站不存在",
			"errorMessage": "检查网站ID是否正确或者返回首页",
		}))
		return
	}

	c.HTML(http.StatusOK, "detail", OutputCommonSession(c, gin.H{
		"Title": "详情分析",
		"Site":  site,
		"Today": today,
	}))
}

// RenderMarkdownFile 文档解析
func (wc *WebController) RenderMarkdownFile(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "文档不存在",
			"errorCode":    "404",
			"errorTitle":   "文档不存在",
			"errorMessage": "检查路径是否正确或者返回首页",
		}))
		return
	}
	// 读取文件内容
	md, err := ioutil.ReadFile("./docs/" + name + ".md")
	if err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "文档不存在",
			"errorCode":    "404",
			"errorTitle":   "文档不存在",
			"errorMessage": "检查路径是否正确或者返回首页",
		}))
		return
	}

	// 渲染 Markdown 为 HTML
	markdown := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
	)
	var buf bytes.Buffer
	if err := markdown.Convert(md, &buf); err != nil {
		c.HTML(http.StatusOK, "404", OutputCommonSession(c, gin.H{
			"Title":        "文档解析错误",
			"errorCode":    "Error",
			"errorTitle":   "文档解析错误",
			"errorMessage": "反馈给我们或者返回首页",
		}))
		return
	}
	content := buf.String()

	c.HTML(http.StatusOK, "docs", OutputCommonSession(c, gin.H{
		"Title":   name + "文档",
		"Content": content,
	}))
}
