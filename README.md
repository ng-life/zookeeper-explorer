# ZooKeeper Explorer

一个用 Go 编写的轻量 Web ZooKeeper 节点浏览器。首页列出多个已配置集群，支持切换集群、逐层浏览节点、查看和下载节点数据，以及递归删除节点。

节点详情支持下载原始数据、按 `.json`/`.yaml`/`.yml` 文件名自动高亮 JSON/YAML，也可以手动切换格式。点击「放大查看」可在大弹窗中浏览数据，字体大小保持不变；支持 Esc、关闭按钮和点击遮罩关闭。支持递归删除节点；操作前会显示完整节点路径并二次确认，根节点 `/` 不允许删除。

节点列表先加载名称和路径；子节点数量、类型和数据大小在列表行进入视口时按需读取，浏览器会缓存成功结果，避免重复请求。

## 项目结构

```text
cmd/zookeeper-explorer/   命令入口、参数解析与退出信号
internal/config/         TOML 配置与命令行覆盖
internal/server/         HTTP 路由、节点操作、连接缓存及服务生命周期
internal/cli/            一次性命令与动态 shell 补全
internal/tui/            Bubble Tea 终端交互、节点读取及数据预览
internal/zookeeper/      ZooKeeper 客户端、路径校验与 chroot 适配
internal/web/            嵌入资源及 assets/index.html
```

测试与所属包放在同一目录。`internal` 包仅供本项目使用，网页仍通过 `go:embed` 打包为单文件程序。

常用命令：`make build`、`make test`、`make vet`、`make fmt`；`make run ARGS='--config config.toml'` 可传入启动参数。

## 配置

复制示例配置并按实际环境修改：

```sh
cp config.example.toml config.toml
```

```toml
listen = "127.0.0.1:8080"
allow_delete = false

[[clusters]]
name = "开发环境"
address = "127.0.0.1:2181"

[[clusters]]
name = "生产集群"
address = "zk-1:2181,zk-2:2181,zk-3:2181"
```

## 启动

需要 Go 1.23 或更高版本。

```sh
go run ./cmd/zookeeper-explorer --config config.toml
```

也可从命令行指定地址（重复传入 `--zk` 可配置多个；格式为 `名称=地址`）：

```sh
go run ./cmd/zookeeper-explorer --listen 127.0.0.1:8080 --zk '开发=127.0.0.1:2181' --zk '测试=zk-a:2181,zk-b:2181'
```

递归删除开关默认关闭。可在 `config.toml` 设置 `allow_delete = true`，或在启动时覆盖配置：

```sh
go run ./cmd/zookeeper-explorer --config config.toml --allow-delete
go run ./cmd/zookeeper-explorer --config config.toml --disable-delete
```

服务端会校验该开关；关闭时网页不显示删除按钮，删除 API 也会拒绝请求。

打开 <http://127.0.0.1:8080>。集群连接在首次浏览时建立。若地址包含 chroot，可按 ZooKeeper 客户端地址格式在端口后附加路径。

## 单文件构建

HTML 已通过 `go:embed` 编译进程序，无需额外发布静态资源目录。构建当前操作系统的 release 可执行文件：

```sh
go build -trimpath -o dist/zookeeper-explorer ./cmd/zookeeper-explorer
```

输出为 `dist/zookeeper-explorer`（Windows 构建时请将输出文件命名为 `zookeeper-explorer.exe`）。将可执行文件和 `config.toml` 放在一起即可运行。

## 验证

```sh
go test -race ./...
go vet ./...
```

默认测试使用模拟客户端，覆盖 API 响应、文本与二进制下载、配置覆盖、chroot 路径转换、删除保护、删除顺序和部分失败。未设置 `ZK_TEST_ADDRESS` 时，真实 ZooKeeper 集成测试会跳过。

集成测试需要一个独立的本地临时 ZooKeeper 实例。测试只接受回环 IP 地址，在唯一的 `/explorer-integration-*` 子树内创建和删除数据，并在结束时清理子树。不要将端口映射到生产集群。

```sh
ZK_TEST_ADDRESS=127.0.0.1:2181 go test -race -count=1 -run TestZooKeeperIntegration -v ./internal/server
```

集成测试覆盖中文文件名、UTF-8 文本、二进制数据、原始下载、chroot、删除开关、递归删除，以及删除后的节点状态回读。

## GitHub Release

推送形如 `v0.1.0` 的标签后，GitHub Actions 会构建并创建 Release，附带 Linux x86_64 与 macOS Apple Silicon arm64 可执行文件：

```sh
git tag v0.1.0
git push origin v0.1.0
```

## 命令行

无子命令时仍启动 Web 服务，也可以显式使用 `serve`。一次性命令直接连接 ZooKeeper，不需要启动 HTTP 服务：

```sh
./dist/zookeeper-explorer clusters --config config.toml
./dist/zookeeper-explorer ls / --cluster test --config config.toml
./dist/zookeeper-explorer get /config/example --cluster test --config config.toml
./dist/zookeeper-explorer stat /config/example --cluster test --config config.toml --output json
```

`clusters` 只列出配置中的名称，不连接远端。多个集群必须用 `--cluster` 选择唯一名称；仅一个集群时可省略。全局参数可放在子命令前后，支持 `--config=文件`。配置读取顺序为：显式 `--config` → 当前目录 `config.toml` → `$XDG_CONFIG_HOME/zookeeper-explorer/config.toml`；未设置 `XDG_CONFIG_HOME` 时回退到 `~/.config/zookeeper-explorer/config.toml`（macOS 也使用此目录）。`--zk` 可覆盖配置中的集群。

`ls PATH` 列出全部直接子节点并排序，不递归、不分页；`get PATH` 默认输出原始字节，不附加换行，可直接重定向到文件。`--output json` 下，`get` 返回 `path`、`data_base64` 和 `stat`，避免损失二进制数据；`stat` 输出 ZooKeeper 元数据。列表的文本输出转义控制字符。数据进入 stdout，错误进入 stderr；成功退出码为 0，失败为 1。`--timeout` 默认 10 秒，覆盖连接与读取操作。CLI 仅提供读取功能，删除开关用于 Web 服务。

### 动态 shell 补全

每次补全重新读取配置；`--cluster` 补全集群名称，`ls`、`get`、`stat` 的路径补全实时读取已选集群的直接子节点，末尾保留 `/` 以便继续补全。远端补全最多等待 2 秒，失败时不返回候选。配置文件路径使用 shell 文件补全。

```sh
# zsh：先初始化 compinit，再加载脚本
./dist/zookeeper-explorer completion zsh > /tmp/zookeeper-explorer.zsh
source /tmp/zookeeper-explorer.zsh

# Bash
./dist/zookeeper-explorer completion bash > /tmp/zookeeper-explorer.bash
source /tmp/zookeeper-explorer.bash
```

在 PATH 中可调用 `zookeeper-explorer`，也可使用 `./dist/zookeeper-explorer`，然后按 Tab：

```sh
./dist/zookeeper-explorer --config config.toml ls --cluster <Tab>
./dist/zookeeper-explorer --config config.toml --cluster test ls /<Tab>
```

本次从 Ubuntu 复制的 systemd 配置保存在 `.local-test/ubuntu.service`，其启动参数转换为 `.local-test/config.toml`，可用 `--config .local-test/config.toml` 联调。这些文件权限为 600，目录已加入 Git 忽略规则；不会随构建分发。


### 交互式终端浏览器

TUI 使用 Bubble Tea、Bubbles 和 Lip Gloss，可直接复用 CLI 配置：

```sh
zookeeper-explorer tui
zookeeper-explorer tui --cluster test
zookeeper-explorer tui /config --cluster test
```

多集群且未指定 `--cluster` 时，先显示集群选择列表，不自动连接第一个集群。单集群时直接进入。宽终端左右排列节点列表与详情；宽度不足 90 列时上下排列，最小尺寸为 60 列 × 16 行。

| 按键 | 操作 |
| --- | --- |
| ↑ / ↓、j / k | 选择节点，预览数据 |
| Enter / → | 进入选中的节点 |
| ← / Backspace | 返回当前节点的父节点 |
| / | 按名称过滤列表；输入时字母作为过滤条件 |
| Esc / Ctrl+G | 清除过滤条件 |
| Tab | 切换节点列表与详情面板 |
| m | 切换数据与 ZooKeeper Stat 元数据 |
| r | 刷新当前节点及子节点列表 |
| c | 返回集群选择列表 |
| q / Ctrl+C | 退出；过滤输入期间使用 Ctrl+C 退出 |

详情面板支持方向键、PageUp / PageDown 滚动。有效 UTF-8 文本直接显示，JSON 自动缩进；二进制数据以十六进制显示。文本预览最多 64 KiB，二进制预览最多 4 KiB，超限时显示截断提示；完整数据可通过 `get PATH` 读取。节点数据中的终端控制字符会转义。

网络请求在后台执行，受 `--timeout` 限制。切换节点、切换集群或退出时取消旧请求，过期结果不覆盖当前页面。TUI 提供只读浏览，需要交互式终端，不支持 `--output json` 或管道输出；脚本调用使用 `ls`、`get`、`stat`。
