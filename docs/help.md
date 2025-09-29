# Pingoo 帮助中心

欢迎来到 **Pingoo 帮助中心**！
这里收集了使用 Pingoo 的常见问题和操作指南，帮你快速上手，让流量统计变得轻松简单。

---

## 如何开始使用 Pingoo？

**自行部署：**

1. 从 GitHub 获取 Pingoo 开源源码。
2. 按照文档部署到你的服务器（Golang 环境）。
3. 在网站中添加 Pingoo 提供的统计脚本。
4. 刷新网站，即可开始统计访问数据。

**SaaS 服务：**

1. 注册 Pingoo 账号。
2. 添加你的网站并生成统计代码。
3. 将代码嵌入网站即可实时查看流量数据。

---

## 统计代码

将以下代码添加到网站 `<head>` 或 `<body>` 中，即可统计网站的基础访问数据（PV/UV、来源、访客地理位置等）：

```html
<script async defer src="网址/pingoo.js" site-id="YOUR_SITE_ID"></script>
```

替换 `YOUR_SITE_ID` 为你在 Pingoo 添加站点后获得的站点 ID。

---

## 自定义事件统计

Pingoo 支持自定义事件统计，用于记录用户的特定操作，例如按钮点击、表单提交等。

### 示例

假设你希望统计用户点击“登陆”按钮的事件，只需要在按钮增加`pingoo-event`属性即可：

```html
<button pingoo-event="login">登陆</button>
```

说明：

* `pingoo-event="login"` 表示一个 **内容为 login** 的自定义事件
* 当用户点击按钮时，Pingoo 会记录一条自定义事件到统计后台
* 你可以在后台查看所有自定义事件，分析用户行为
* 如果你希望记录不同类型的事件，Pingoo 默认的机制就是统一事件名为 custom，通过 pingoo-event 的值区分不同操作

### 高级用法

* 可以为任意 HTML 元素添加 `pingoo-event` 属性进行事件统计
* 自定义事件可用于分析关键行为、A/B 测试、用户行为路径等

---

## Pingoo 可以统计哪些数据？

* PV/UV（页面访问量/独立访客）
* 访问来源（搜索引擎、外部链接等）
* 访客地理位置（城市、国家级别）
* 设备类型、操作系统、网络类型
* 用户操作事件（点击、表单提交等）

所有数据均 **匿名统计，不收集个人身份信息**。

---

## API 文档

Pingoo 提供简单易用的 **REST API**，方便你获取统计数据或二次开发。

* **API 文档**：[查看 Pingoo API 文档](/docs/api)
* 你可以通过 API 获取 PV/UV、事件数据等，实现自定义报表和分析。

---

## 关于 Pingoo

想了解 Pingoo 的名字由来、设计理念和开发初衷？请访问：

* **About 页面**：[了解 Pingoo](/docs/about)

---

## 常见问题

**Q1：Pingoo 会影响网站性能吗？**

A1：不会。Pingoo 轻量小巧，脚本体积小，部署后不会拖慢网站加载。

**Q2：如何保证访客隐私？**

A2：Pingoo 完全匿名统计，不记录姓名、邮箱等敏感信息。

**Q3：数据可以导出吗？**

A3：未来版本将支持数据导出功能，方便进一步分析。

**Q4：SaaS 服务出现问题怎么办？**

A4：请通过官网联系方式联系我们，我们会尽快排查解决。

---

## 联系我们

如果遇到任何问题或有建议，欢迎随时联系我们：

* 邮箱：[twothreewang@gmail.com](mailto:twothreewang@gmail.com)
* GitHub Issues：[Pingoo 仓库](https://github.com/TwoThreeWang/pingoo/issues)

---

最后更新日期：2025年9月29日

**感谢您选择 Pingoo 分析！我们致力于为您提供最佳的分析体验。如需更多帮助，请随时联系我们。**