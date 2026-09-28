# ZooKeeper Explorer

一个用 Rust 编写的轻量 Web ZooKeeper 节点浏览器。首页列出已配置集群，支持多个集群切换、逐层浏览节点、查看节点数据。当前提供只读操作。

## 配置

复制示例配置并按实际环境修改：

```sh
cp config.example.toml config.toml
```

```toml
listen = "127.0.0.1:8080"

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

打开 <http://127.0.0.1:8080>。集群连接在首次浏览时建立。若地址包含 chroot，可按 ZooKeeper 客户端地址格式在端口后附加路径。
