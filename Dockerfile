# 使用官方的 Go 镜像作为构建阶段
FROM golang:1.21 as builder

# 设置工作目录
WORKDIR /meson-monitor

# 将当前目录下所有文件复制到工作目录
COPY . .

# 编译 Go 程序
RUN go build -o bridge-monitor main.go

# 使用更小的 Ubuntu 镜像作为基础镜像
FROM ubuntu:latest

# 安装必要的工具和库
RUN apt-get update && apt-get install -y \
    bash \
    ca-certificates \
    vim \
    procps \
    && rm -rf /var/lib/apt/lists/*

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制编译后的二进制文件
COPY --from=builder /meson-monitor/bridge-monitor .
COPY config.json .
COPY start_and_monitor.sh .

# 创建last_block目录
RUN mkdir /root/last_block

# 给予脚本执行权限
RUN chmod +x start_and_monitor.sh

# 设置启动命令为进入无限期等待状态
ENTRYPOINT ["bash", "-c", "tail -f /dev/null"]
