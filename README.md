# ZooKeeper Explorer

一个用 Rust 编写的轻量 Web ZooKeeper 节点浏览器。首页列出多个已配置集群，支持切换集群、逐层浏览节点、查看和下载节点数据，以及递归删除节点。

节点详情支持下载原始数据、按 `.json`/`.yaml`/`.yml` 文件名自动高亮 JSON/YAML，也可以手动切换格式。支持递归删除节点；操作前会显示完整节点路径并二次确认，根节点 `/` 不允许删除。

节点列表先加载名称和路径；子节点数量、类型和数据大小在列表行进入视口时按需读取，浏览器会缓存成功结果，避免重复请求。

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

```sh
cargo run -- --config config.toml
```

也可从命令行指定地址（重复传入 `--zk` 可配置多个；格式为 `名称=地址`）：

```sh
cargo run -- --listen 127.0.0.1:8080 --zk '开发=127.0.0.1:2181' --zk '测试=zk-a:2181,zk-b:2181'
```

递归删除开关默认关闭。可在 `config.toml` 设置 `allow_delete = true`，或在启动时覆盖配置：

```sh
cargo run -- --config config.toml --allow-delete
cargo run -- --config config.toml --disable-delete
```

服务端会校验该开关；关闭时网页不显示删除按钮，删除 API 也会拒绝请求。

打开 <http://127.0.0.1:8080>。集群连接在首次浏览时建立。若地址包含 chroot，可按 ZooKeeper 客户端地址格式在端口后附加路径。

## 单文件构建

HTML 已通过 `include_str!` 编译进程序，无需额外发布静态资源目录。构建当前操作系统的 release 可执行文件：

```sh
cargo build --release
```

输出为 `target/release/zookeeper-explorer`（Windows 上为 `target/release/zookeeper-explorer.exe`）。将可执行文件和 `config.toml` 放在一起即可运行。

## GitHub Release

推送形如 `v0.1.0` 的标签后，GitHub Actions 会构建并创建 Release，附带 Linux x86_64 与 macOS Apple Silicon arm64 可执行文件：

```sh
git tag v0.1.0
git push origin v0.1.0
```
