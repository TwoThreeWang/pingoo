package controllers

import (
	"html/template"
	"os"
	"path/filepath"
	"pingoo/config"
	"pingoo/middleware"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
)

func LoadLocalTemplates(templatesDir string) render.HTMLRender {
	r := multitemplate.NewRenderer()

	// 获取所有模板文件
	base := templatesDir + "/base.html"
	templates := []string{}

	// 使用filepath.Walk遍历templates目录
	err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 只处理.html文件，且排除base.html
		if !info.IsDir() && filepath.Ext(path) == ".html" && filepath.Base(path) != "base.html" {
			templates = append(templates, path)
		}
		return nil
	})

	if err != nil {
		panic(err)
	}

	// 为每个页面配置模板，并应用模板函数
	for _, tmpl := range templates {
		name := filepath.Base(tmpl)
		name = name[:len(name)-len(filepath.Ext(name))]

		// 创建一个新的模板，并应用函数映射
		t := template.New(filepath.Base(base)).Funcs(templateFun())
		t = template.Must(t.ParseFiles(base, tmpl))
		r.Add(name, t)
	}

	return r
}

func templateFun() template.FuncMap {
	return template.FuncMap{
		"safeHTML": func(str string) template.HTML {
			return template.HTML(str)
		},
	}
}

func OutputCommonSession(c *gin.Context, h ...gin.H) gin.H {
	result := gin.H{}
	cfg := config.GetConfig()
	// 从上下文中获取用户信息
	userInfo := middleware.GetCurrentUser(c)
	result["userInfo"] = userInfo
	result["siteUrl"] = cfg.Site.SiteUrl
	result["siteName"] = cfg.Site.SiteName
	result["version"] = cfg.Site.VERSION
	result["refer"] = c.Request.Referer()
	result["path"] = c.Request.URL.Path
	result["trackerScriptName"] = cfg.Site.TrackerScriptName
	for _, v := range h {
		for k1, v1 := range v {
			result[k1] = v1
		}
	}
	return result
}
