#!/usr/bin/env bash
# MySQL 首次初始化：为应用账号授予全部权限
#
# 仅在数据卷为空（首次 task infra）时由 mysql 镜像入口执行；
# 应用账号（MYSQL_USER）由入口脚本先行创建，本脚本只负责授权。
# 授权范围为 *.*（含 CREATE DATABASE）：应用需自行创建 ganrag_* 库（由 pkg/db 迁移执行），
# 开发环境单实例授予全部权限以省去按库授权与未来新库的重复配置。
set -euo pipefail

mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" <<SQL
GRANT ALL PRIVILEGES ON *.* TO '${MYSQL_USER}'@'%' WITH GRANT OPTION;
FLUSH PRIVILEGES;
SQL

echo "已为应用账号 ${MYSQL_USER} 授予全部权限"
