# clash-meta / mihomo linux-amd64 自举包

本包用于 Linux amd64，无 Docker。包含：

- `core.sh`：使用 `nohup` 管理本地 mihomo 进程，支持 `start` / `stop` / `restart` / `status`
- `init.sh`：首次初始化向导，输入订阅、更新配置并选择后台运行方式
- `update-config.sh`：拉取订阅并生成 `config/config.yaml`
- `scripts/`：核心安装、配置合并、运行控制、代理环境等内部脚本
- `config/ui/metacubexd/`：MetaCubeXD Web 面板
- `config/ui/yacd/`：Yacd Web 面板
- `proxy-env.sh`：手动给当前 shell 设置或清理代理环境变量
- `systemd/mihomo.service.template`：可选 systemd service 模板

## 使用

```bash
tar -xzf clash_mihomo-lite-20260702.tar.gz
cd clash_mihomo
chmod +x *.sh scripts/*.sh scripts/*.py
./init.sh
```

启动成功后：

- MetaCubeXD：http://127.0.0.1:19090/ui/metacubexd/
- Yacd：http://127.0.0.1:19090/ui/yacd/
- mixed-port：http/socks 共用 `127.0.0.1:17890`
- HTTP：`127.0.0.1:17890`
- SOCKS：`127.0.0.1:17890`

`core.sh start` 默认使用 `nohup` 后台启动；如果要交给系统服务托管，请使用下面的 systemd 模板，不要同时用两种方式启动同一个目录。

也可以非交互初始化：

```bash
./init.sh <SUB_URL>
./init.sh <SUB_URL> nohup
./init.sh <SUB_URL> systemd
```

## 更新订阅

```bash
./update-config.sh <SUB_URL>
./core.sh restart
```

也可以把订阅地址写入 `config/subscription.url`，或用环境变量：

```bash
CLASH_SUB_URL=<SUB_URL> ./update-config.sh
```

## 让当前终端走代理

```bash
source ./proxy-env.sh
```

取消：

```bash
source ./proxy-env.sh unset
```

## systemd 可选

`./init.sh` 已经可以选择 systemd。下面是手动安装方式。

先确认已生成 `config/config.yaml`，或已写好 `config/subscription.url`。

```bash
ABS=$(pwd)
sed "s#<ABS_PATH>#$ABS#g" systemd/mihomo.service.template | sudo tee /etc/systemd/system/mihomo.service
sudo systemctl daemon-reload
sudo systemctl enable --now mihomo
```

## 说明

发布包不内置订阅、生成后的 `config.yaml`、运行日志或缓存。`bin/mihomo` 也不随包内嵌；首次运行 `./core.sh start` 会自动下载当前锁定版本 `v1.19.27` 的 `mihomo-linux-amd64-v1.19.27.gz` 并解压为可执行文件。
