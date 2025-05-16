# kubelogdetails

一个用于查看Kubernetes Pod日志的终端工具，可以同时显示Pod所属控制器下的所有Pod的实时日志。

## 功能特点

- 自动查找Pod所属的控制器（Deployment/StatefulSet/DaemonSet）
- 显示控制器下所有Pod的实时日志
- 在终端中分区域显示每个Pod的日志
- 支持实时更新日志内容

## 安装

```bash
go install github.com/laik/kubelogdetails@latest
```

## 使用方法

```bash
# 查看指定Pod的日志
kubelogdetails <pod-name>

# 指定命名空间
kubelogdetails -n <namespace> <pod-name>
```

## 快捷键

- `q`: 退出程序

## 依赖

- Go 1.24+
- Kubernetes集群访问权限
- 本地kubeconfig配置

## 构建

```bash
git clone https://github.com/laik/kubelogdetails.git
cd kubelogdetails
go build
``` 