package web

import "net/http"

// release 是更新日志里的一个版本。新版本加在 changelog 最前面，Version（页脚、设置页显示的版本号）自动跟着变。
// 版本号：大改版加第一位，有新功能加第二位，只有修复加第三位。旧版（标签 legacy-v1）到 v1.0.8 为止，
// 新版 Pingoo 从 v2.0.0 开始。这里写给用户看：只写他们用得到、看得见的变化，部署、重构这类不写。
type release struct {
	Version string
	Date    string // 2006-01-02
	Summary string
	Groups  []releaseGroup
}

type releaseGroup struct {
	Name  string // 新增 / 改进 / 修复
	Items []string
}

var changelog = []release{
	{Version: "2.2.0", Date: "2026-11-15", Summary: "时间显示统一按你的时区，核心指标多了浏览量。",
		Groups: []releaseGroup{
			{"新增", []string{
				"网站详情的核心指标在“访客”后面加了“浏览量”，点一下就能切换趋势图。",
				"更新日志页面（就是这一页），页脚有入口。",
			}},
			{"改进", []string{
				"隐私政策补充说明：首页、帮助等对外页面用 Google Analytics 统计访问量；登录后的页面不加载任何第三方统计。",
			}},
			{"修复", []string{
				"界面上的时间有时按 UTC 显示，比实际早或晚了几个小时（例如“AI 生成于 今天 13:07”）。现在积分明细、同步记录、套餐到期、邮件里的时间都按默认时区显示。",
				"手机上展开问答的对话列表后只能再点菜单才能收起。现在点空白处、按 Esc 或选中一个对话都会收起。",
			}},
		}},
	{Version: "2.1.0", Date: "2026-11-14", Summary: "诊断可以中途停止，邮件换了新样式，AI 慢回答不再超时失败。",
		Groups: []releaseGroup{
			{"新增", []string{
				"诊断还没出结果时关掉窗口，会停止这次诊断，只按已经用掉的部分扣积分；刚开始就关掉不扣。",
				"设置页的兑换框旁边和积分明细页可以直接去购买积分卡密。",
			}},
			{"改进", []string{
				"所有邮件（初始密码、找回密码、验证码、每日简报、到期提醒）换成统一的简洁样式，手机上也好读。",
			}},
			{"修复", []string{
				"推理类模型思考时间较长时，回答会被中途掐断，最后报“context deadline exceeded”。现在只要模型还在输出就不会被打断。",
			}},
		}},
	{Version: "2.0.0", Date: "2026-11-14", Summary: "全新的 Pingoo：读取 Google Analytics 和 Search Console，用 AI 讲清网站数据的变化。",
		Groups: []releaseGroup{
			{"新增", []string{
				"全部网站概览：一句话 AI 简报、访客、搜索点击、在线人数、“需要关注”列表，每个网站一张小图，图上标出变化的原因。",
				"网站详情：AI 洞察和待办、核心指标、内容表现、流量来源（含 AI 助手来源、新外链）、搜索表现、访客画像、健康检查。",
				"AI 诊断：从异常点、洞察或单个页面发起，AI 一步步查数据，给出结论、置信度和建议。",
				"AI 问答、自动生成的周报和月报，报告会回头检查上期建议的效果。",
				"单页分析、日期范围和对比（上一期或去年同期）、Google 搜索算法更新标注在趋势图上。",
				"放了 AdSense 的网站可以看到广告收益、RPM 和收入最高的页面。",
				"积分按 AI 实际用量扣，失败全额退回；免费版、基础版、专业版三档套餐，用卡密开通。",
				"只用邮箱注册，不用第三方验证码；没注册也可以先看演示账号。",
			}},
		}},
}

// Version 是当前版本号，取更新日志里最新的一个。
var Version = changelog[0].Version

func (s *Server) changelogPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "changelog", "更新日志", "", changelog)
}
