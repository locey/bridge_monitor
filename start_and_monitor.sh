#!/bin/bash

# 启动程序的函数
start_program() {
    echo "Starting bridge_monitor..." | tee -a app.log
    ./bridge-monitor >> app.log 2>&1 &
    echo $! > bridge_monitor.pid
    echo "bridge_monitor started with PID $(cat bridge_monitor.pid)" | tee -a app.log
}

# 检查程序是否运行的函数
check_program() {
    if [ -f bridge_monitor.pid ]; then
        PID=$(cat bridge_monitor.pid)
        if ps -p $PID > /dev/null; then
            echo "bridge_monitor is running with PID $PID" | tee -a app.log
        else
            echo "bridge_monitor is not running. Starting it..." | tee -a app.log
            start_program
        fi
    else
        echo "No PID file found. Starting bridge_monitor..." | tee -a app.log
        start_program
    fi
}

# 检查并备份日志文件的函数
check_log_file() {
    LOG_FILE="app.log"
    MAX_SIZE=$((1 * 1024 * 1024)) # 1M

    if [ -f "$LOG_FILE" ]; then
        FILE_SIZE=$(stat -c%s "$LOG_FILE")
        if (( FILE_SIZE > MAX_SIZE )); then
            TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
            cp "$LOG_FILE" "app_$TIMESTAMP.log"
            echo "Log file backed up as app_$TIMESTAMP.log" | tee -a app.log
            > "$LOG_FILE" # 清空日志文件内容
        fi
    fi
}

# 启动程序
start_program

# 看门狗循环，每分钟检查一次
while true; do
    sleep 60
    check_program
    check_log_file
done
