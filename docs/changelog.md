# Pingoo 更新记录

所有重要的更新记录都在此文档中列出。

![骄傲版本控制](https://cdn.c2v2.com/imgur/KfG7NBr.png)

## [2025-09-30] - v1.0.2
- Added
  * 新增 `daily_stats` 表，用于按天聚合各维度（OS、Browser、Device、Page、Referrer）的 PV 数据。
  * 实现批量 Upsert 功能，将事件数据高效写入 `daily_stats`。
  * 新增浏览器统计指标

- Changed
  * 统计逻辑优化：部分频繁查询直接从 `daily_stats` 表读取，减少事件表全表扫描，提高查询性能。

- Fixed
  * 修复 Referrer 统计时子域名/路径分散问题，统一为主域名，空值归为“直接访问”。

- Deprecated
  * 无

- Removed
  * 无

## [2025-09-29] - v1.0.1
- 优化：
  - 统一指标命名以提高可读性
  - 实现 IP 匿名化

## [2025-09-29] - v1.0.0
- 初始版本发布：
  - 网站基础功能上线
  - API 功能完善

---

最后更新日期：2025年9月30日