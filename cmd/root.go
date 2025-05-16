package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	namespace string
	podName   string
)

var rootCmd = &cobra.Command{
	Use:   "kubelogdetails",
	Short: "A tool to show pod logs with their controller information",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("pod name is required")
		}
		podName = args[0]
		return nil
	},
}

func Execute(ctx context.Context, clientset *kubernetes.Clientset) error {
	rootCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to use")

	if err := rootCmd.Execute(); err != nil {
		return err
	}

	// 获取pod信息
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting pod: %v", err)
	}

	// 查找pod的控制器
	var controllerName string
	var controllerType string
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			rs, err := clientset.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			for _, rsRef := range rs.OwnerReferences {
				if rsRef.Kind == "Deployment" {
					controllerName = rsRef.Name
					controllerType = "Deployment"
					break
				}
			}
		} else if ref.Kind == "StatefulSet" {
			controllerName = ref.Name
			controllerType = "StatefulSet"
		} else if ref.Kind == "DaemonSet" {
			controllerName = ref.Name
			controllerType = "DaemonSet"
		}
	}

	fmt.Printf("Found controller: %s (%s)\n", controllerName, controllerType)

	// 获取所有相关的pod
	var pods []string
	if controllerName != "" {
		labelSelector := fmt.Sprintf("app=%s", controllerName)
		podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("error listing pods: %v", err)
		}
		for _, p := range podList.Items {
			pods = append(pods, p.Name)
		}
	} else {
		pods = []string{podName}
	}

	// 初始化UI
	if err := termui.Init(); err != nil {
		return fmt.Errorf("failed to initialize termui: %v", err)
	}
	defer termui.Close()

	// 创建顶部信息栏
	header := widgets.NewParagraph()
	header.Text = fmt.Sprintf("Found controller: %s (%s)", controllerName, controllerType)
	header.Border = false

	// 创建日志显示区域
	grid := termui.NewGrid()
	termWidth, termHeight := termui.TerminalDimensions()
	grid.SetRect(0, 0, termWidth, termHeight)

	// 为每个pod创建一个日志显示区域
	logWidgets := make([]*widgets.Paragraph, len(pods))
	for i, pod := range pods {
		logWidgets[i] = widgets.NewParagraph()
		logWidgets[i].Title = pod
		logWidgets[i].Text = fmt.Sprintf("Loading logs for %s...", pod)
	}

	// 设置网格布局（每行固定2个pod）
	n := len(pods)
	if n == 0 {
		return fmt.Errorf("no pods found")
	}
	cols := 2
	rowsCount := (n + cols - 1) / cols

	rows := make([]interface{}, 0, rowsCount)
	for r := 0; r < rowsCount; r++ {
		colsWidgets := make([]interface{}, 0, cols)
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				break
			}
			colsWidgets = append(colsWidgets, termui.NewCol(1.0/float64(cols), logWidgets[idx]))
		}
		rows = append(rows, termui.NewRow(1.0/float64(rowsCount), colsWidgets...))
	}
	// 顶部信息栏占10%，其余90%为日志
	grid.Set(
		termui.NewRow(0.1, header),
		termui.NewRow(0.9, rows...),
	)

	// 显示UI
	termui.Render(grid)

	// 为每个pod启动日志流
	for i, pod := range pods {
		go func(index int, podName string) {
			req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
				Follow:    true,
				TailLines: func() *int64 { n := int64(200); return &n }(),
			})
			stream, err := req.Stream(ctx)
			if err != nil {
				logWidgets[index].Text = fmt.Sprintf("Error getting logs: %v", err)
				termui.Render(grid)
				return
			}
			defer stream.Close()

			buf := make([]byte, 1024)
			for {
				n, err := stream.Read(buf)
				if err != nil {
					break
				}
				// 将日志内容按行分割，并确保每行在窗口内自动换行
				lines := strings.Split(string(buf[:n]), "\n")
				for i, line := range lines {
					if i > 0 {
						lines[i] = "\n" + line
					}
				}
				logWidgets[index].Text = strings.Join(lines, "")
				termui.Render(grid)
			}
		}(i, pod)
	}

	// 等待用户退出
	uiEvents := termui.PollEvents()
	for {
		e := <-uiEvents
		if e.Type == termui.KeyboardEvent && e.ID == "q" {
			break
		}
		if e.Type == termui.ResizeEvent {
			termWidth, termHeight := termui.TerminalDimensions()
			grid.SetRect(0, 0, termWidth, termHeight)
			// 重新计算布局
			cols := 2
			rowsCount := (n + cols - 1) / cols

			rows := make([]interface{}, 0, rowsCount)
			for r := 0; r < rowsCount; r++ {
				colsWidgets := make([]interface{}, 0, cols)
				for c := 0; c < cols; c++ {
					idx := r*cols + c
					if idx >= n {
						break
					}
					colsWidgets = append(colsWidgets, termui.NewCol(1.0/float64(cols), logWidgets[idx]))
				}
				rows = append(rows, termui.NewRow(1.0/float64(rowsCount), colsWidgets...))
			}
			grid.Set(
				termui.NewRow(0.1, header),
				termui.NewRow(0.9, rows...),
			)
			termui.Render(grid)
		}
	}

	return nil
}
