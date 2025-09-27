# Pingoo 🐧 - 轻量、隐私友好的网站统计分析系统

**嗨，欢迎来到 Pingoo！**

Pingoo 是一款轻量、高效、易用、隐私友好的网站流量统计工具，专为希望快速了解网站访问情况的开发者、博主和小型网站打造。我们相信，统计数据应该简单、清晰、快速，而不是繁琐、复杂、臃肿。

基于 Gin 框架构建，提供事件追踪、页面浏览统计和用户行为分析功能。专为中小型网站设计，易于部署和集成。

## ✨ 核心特点

### 🚀 轻量快速
- **体积小、部署快**：资源占用极低，不拖慢网站加载速度
- **零依赖**：简单配置即可运行，无需复杂环境
- **快速响应**：实时数据处理，即时查看统计数据

### 📊 实时统计
- **PV/UV 统计**：页面浏览量和独立访客数一目了然
- **访问来源分析**：搜索引擎、社交媒体、直接访问等来源统计
- **访客地理位置**：全球访客分布可视化
- **自定义时间范围**：灵活查看任意时间段的数据

### 👥 访客分析
- **设备类型**：桌面、平板、手机设备统计
- **操作系统**：Windows、macOS、Linux、iOS、Android
- **浏览器分析**：Chrome、Firefox、Safari、Edge 等
- **网络类型**：移动、联通、电信等网络环境统计

### 🔍 事件追踪
- **页面浏览追踪**：自动记录每个页面的访问
- **自定义事件**：点击、表单提交等关键操作追踪
- **用户行为分析**：会话管理和访问路径分析

### 🔒 隐私友好
- **不收集个人数据**：完全匿名统计，保护用户隐私
- **数据自主控制**：支持自主删除数据
- **开源透明**：代码完全开源，数据安全可控

## 🛠️ 系统功能

- **RESTful API**：支持第三方集成和自定义开发
- **JWT 认证**：安全的用户认证和权限控制
- **多站点管理**：支持多个网站的数据隔离统计
- **响应式界面**：适配桌面和移动设备的现代化界面
- **Docker 支持**：一键容器化部署

## 🛠️ 技术栈

Pingoo 采用现代化的技术栈构建，确保系统的高性能和易维护性：

### 🔧 后端技术
- **Go 1.21+** - 高性能的编译型语言
- **Gin 框架** - 轻量级 Web 框架，快速路由处理
- **GORM** - 强大的 ORM 库，支持多种数据库

### ⚙️ 配置管理
- **环境变量** - 灵活的配置方式
- **godotenv** - 优雅的 .env 文件支持

### 📡 API 设计
- **RESTful API** - 标准的 REST 接口设计
- **JWT 认证** - 安全的用户认证机制
- **CORS 支持** - 跨域资源共享支持

## 🚀 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/TwoThreeWang/pingoo.git
cd pingoo
```

### 2. 环境要求

- **Go 1.21+** - 确保已安装最新版本的 Go
- **PostgreSQL** - 推荐使用 PostgreSQL 数据库

### 3. 安装依赖

```bash
go mod download
```

### 4. 配置环境变量

复制 `.env.example` 为 `.env` 并根据需要修改配置：

```bash
cp .env.example .env
```

编辑 `.env` 文件，根据你的环境进行配置：

```bash
# 服务器配置
SERVER_PORT=5004    # 服务运行端口
GIN_MODE=debug      # 运行模式（debug/release）

# 网站配置
SITE_NAME=Pingoo                  # 网站显示名称
SITE_DOMAIN=http://localhost:5004 # 网站访问域名
VERSION=1.0.0                     # 程序版本
TRACKER_SCRIPT_NAME=pingoo.js     # 追踪脚本名称（防止被广告拦截）

# PostgreSQL 数据库配置
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=pingoo
DB_SSLMODE=disable

# JWT 认证配置
JWT_SECRET_KEY=your-secret-key-here  # 请修改为强密码
JWT_EXPIRE_HOURS=72
JWT_REFRESH_EXPIRE=168
```

### 5. 启动服务

```bash
go run main.go
```

🎉 **恭喜！** 服务将在 `http://localhost:5004` 启动，你可以访问 Pingoo 的管理界面了。

### 6. 🎯 使用教程

只需简单几步，即可开始使用 Pingoo 统计你的网站：

1. **创建账户**：通过网页界面或 API 注册用户账户
2. **添加网站**：在管理后台添加要统计的网站，获取唯一的 SiteID
3. **集成代码**：将生成的统计代码添加到你的网站中
4. **查看数据**：访问你的网站，然后在 Pingoo 仪表盘中查看实时统计数据

#### 📝 统计代码集成

将以下代码添加到你的网站 `<head>` 标签内：

```html
<!DOCTYPE html>
<html>
<head>
    <!-- Pingoo 统计代码 -->
    <script src='http://localhost:5004/pingoo.js' site-id='你的站点ID'></script>
    <title>我的网站</title>
</head>
<body>
    <!-- 你的网站内容 -->
</body>
</html>
```

💡 **提示**：将 `你的站点ID` 替换为你在 Pingoo 后台获取的实际 SiteID。

## API文档

详细API文档请参考 [API.md](docs/api.md) 文件，包含以下主要内容：

- **用户认证API**：注册、登录、获取当前用户信息
- **站点管理API**：创建站点、获取站点列表、站点详情、站点统计
- **事件追踪API**：页面浏览追踪、自定义事件追踪
- **前端埋点代码**：自动生成的追踪代码集成指南
- **错误处理**：各种错误情况的响应格式

## 📁 项目结构

Pingoo 采用清晰的分层架构，代码组织规范，便于维护和扩展：

```
pingoo/
├── main.go                 # 主程序入口
├── config/                 # 配置文件
│   └── config.go          # 配置结构体
├── controllers/           # 控制器层
│   ├── auth_controller.go  # 认证控制器（注册、登录、用户信息）
│   ├── event_controller.go # 事件控制器
│   ├── render.go          # 渲染控制器
│   ├── site_controller.go # 站点管理控制器
│   └── web_controller.go  # Web页面控制器
├── database/              # 数据库相关
│   ├── database.go        # 数据库连接
│   └── migrations.go      # 数据库迁移
├── middleware/            # 中间件
│   ├── auth.go            # JWT认证中间件
│   └── cors.go            # CORS中间件
├── models/                # 数据模型
│   ├── event.go           # 事件模型
│   ├── session.go         # 会话模型
│   ├── site.go            # 站点模型
│   ├── stats.go           # 统计模型
│   └── user.go            # 用户模型
├── routers/               # 路由配置
│   └── router.go          # 路由设置
├── services/              # 业务逻辑层
│   ├── event_service.go   # 事件服务
│   └── site_service.go    # 站点服务
├── utils/                 # 工具函数
│   ├── response.go        # 响应工具
│   └── ua_parser.go       # UserAgent解析
├── templates/             # HTML模板
│   ├── base.html          # 基础模板
│   ├── index.html         # 首页
│   ├── dashboard.html     # 仪表盘
│   ├── login.html         # 登录页面
│   ├── register.html      # 注册页面
│   ├── profile.html       # 个人资料页面
│   ├── websites.html      # 站点管理页面
│   └── 404.html           # 404页面
├── public/                # 静态资源
│   ├── css/               # 样式文件
│   ├── js/                # JavaScript文件
│   ├── pingoo.js          # 前端埋点SDK
│   └── img/               # 图片资源
├── docs/                  # 文档目录
├── .env                   # 配置文件
├── .env.example           # 环境变量示例
├── go.mod                 # Go模块文件
├── go.sum                 # Go依赖锁定文件
├── README.md              # 项目说明
├── LICENSE                # 许可证文件
├── Dockerfile             # Docker配置
├── docker-compose.yml     # Docker Compose配置
└── deploy.sh              # 部署脚本
```

### 🏗️ 架构说明

- **控制器层**：处理 HTTP 请求和响应
- **服务层**：封装业务逻辑和数据处理
- **模型层**：定义数据结构和数据库操作
- **中间件**：处理认证、跨域等通用功能
- **工具函数**：提供通用的辅助功能

## 🔧 开发指南

### 🛠️ 环境搭建

1. **安装 Go 1.21+** - 确保使用最新版本的 Go
2. **安装 PostgreSQL** - 推荐使用 PostgreSQL 数据库
3. **配置环境变量** - 复制 `.env.example` 为 `.env` 并修改配置
4. **启动开发服务器** - 运行 `go run main.go`

### 📝 代码规范

Pingoo 遵循以下代码规范：

- **Go 官方规范** - 严格遵循 Go 语言官方代码规范
- **代码格式化** - 使用 `gofmt` 自动格式化代码
- **注释要求** - 为公共函数和复杂逻辑添加必要的注释
- **错误处理** - 使用 Go 的错误处理最佳实践

### 📦 构建与部署

```bash
# 🔨 构建可执行文件
go build -o pingoo main.go

# 🐳 Docker 构建
docker build -t pingoo .

# 🚀 使用 Docker Compose 部署
docker-compose up -d
```

### 🎯 添加新功能

1. **添加新API端点**:
   - 在 `routers/router.go` 中添加路由
   - 在 `controllers/` 中创建对应的控制器
   - 在 `services/` 中创建对应的业务逻辑

2. **添加新数据模型**:
   - 在 `models/` 中创建新的模型结构
   - 使用GORM标签定义数据库字段
   - 在 `main.go` 中自动迁移数据库

## 🤝 贡献指南

我们热忱欢迎社区贡献！请遵循以下流程：

### 🚀 贡献流程

1. **Fork 项目** - 在 GitHub 上 Fork 本项目
2. **创建分支** - 创建功能分支 (`git checkout -b feature/AmazingFeature`)
3. **提交更改** - 提交清晰的提交信息 (`git commit -m 'Add some AmazingFeature'`)
4. **推送分支** - 推送到你的分支 (`git push origin feature/AmazingFeature`)
5. **创建 PR** - 创建 Pull Request 并描述你的改进

### 📋 贡献类型

- **Bug 修复** - 报告和修复发现的 Bug
- **功能改进** - 优化现有功能或添加新功能
- **文档完善** - 改进文档、添加示例
- **性能优化** - 优化系统性能

## 📄 许可证

本项目采用 **MIT 许可证** - 详见 [LICENSE](LICENSE) 文件

MIT 许可证赋予你使用、复制、修改、合并、发布、分发、再许可和/或销售本软件副本的权利，但需在软件副本中包含上述版权声明和本许可声明。

## 🎯 使用场景

Pingoo 特别适合以下场景：

- **个人博客**：了解读者来源和阅读习惯
- **小型企业网站**：分析客户访问行为和转化率
- **开源项目文档**：跟踪文档使用情况和用户需求
- **产品展示页面**：优化页面设计和用户体验

## 💬 社区与支持

### 📞 联系方式

- **项目地址**: [https://github.com/TwoThreeWang/pingoo](https://github.com/TwoThreeWang/pingoo)
- **问题反馈**: [Pingoo Issues](https://github.com/TwoThreeWang/pingoo/issues)
- **讨论交流**: 欢迎在 GitHub Discussions 中交流使用经验

### 🤝 贡献指南

我们欢迎各种形式的贡献！如果你有任何想法或改进建议：

1. **报告问题**：在 Issues 中描述你遇到的问题
2. **功能建议**：告诉我们你希望看到的新功能
3. **代码贡献**：提交 Pull Request 来改进代码
4. **文档完善**：帮助改进文档和示例

### 🌟 致谢

感谢所有使用和支持 Pingoo 的用户！你们的反馈是我们持续改进的动力。