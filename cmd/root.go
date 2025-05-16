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
	"k8s.io/client-go/tools/clientcmd"
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
	// 从kubeconfig配置中读取默认namespace
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	ns, _, err := kubeConfig.Namespace()
	if err != nil {
		return fmt.Errorf("error getting namespace from kubeconfig: %v", err)
	}
	namespace = ns

	rootCmd.Flags().StringVarP(&namespace, "namespace", "n", namespace, "namespace to use")

	if err := rootCmd.Execute(); err != nil {
		return err
	}

	// 获取pod信息
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		// 如果找不到pod，尝试从当前命名空间重新获取pod列表
		podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("error listing pods: %v", err)
		}
		if len(podList.Items) == 0 {
			return fmt.Errorf("no pods found in namespace %s", namespace)
		}
		fmt.Printf("Pod %s not found in namespace %s. Available pods:\n", podName, namespace)
		for _, p := range podList.Items {
			fmt.Printf("- %s\n", p.Name)
		}
		return fmt.Errorf("please select a valid pod from the list above")
	}

	// 查找pod的控制器
	var controllerName, controllerType, controllerAPIVersion string
	for _, ref := range pod.OwnerReferences {
		controllerName = ref.Name
		controllerType = ref.Kind
		controllerAPIVersion = ref.APIVersion
		break // 只取第一个 owner
	}

	// 判断是否为原生资源
	isNative := false
	switch controllerType {
	case "StatefulSet", "Deployment", "DaemonSet":
		if controllerAPIVersion == "apps/v1" {
			isNative = true
		}
	case "Job", "CronJob":
		if controllerAPIVersion == "batch/v1" {
			isNative = true
		}
	}
	if !isNative {
		controllerType = "CRD"
	}

	fmt.Printf("Found controller: %s (%s)\n", controllerName, controllerType)

	// 获取所有相关的pod
	var pods []string
	if controllerName != "" {
		var labelSelector string
		if controllerType == "StatefulSet" {
			labelSelector = fmt.Sprintf("statefulset.kubernetes.io/pod-name=%s", podName)
		} else if controllerType == "Job" {
			labelSelector = fmt.Sprintf("job-name=%s", controllerName)
		} else if controllerType == "CronJob" {
			labelSelector = fmt.Sprintf("cronjob-name=%s", controllerName)
		} else if controllerType == "Deployment" {
			labelSelector = fmt.Sprintf("app=%s", controllerName)
		} else if controllerType == "DaemonSet" {
			// Try both name and k8s-app labels for DaemonSets
			labelSelectors := []string{
				fmt.Sprintf("name=%s", controllerName),
				fmt.Sprintf("k8s-app=%s", controllerName),
			}

			foundPods := false
			for _, selector := range labelSelectors {
				fmt.Printf("Trying label selector: %s in namespace: %s\n", selector, namespace)
				podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
					LabelSelector: selector,
				})
				if err != nil {
					continue
				}
				if len(podList.Items) > 0 {
					for _, p := range podList.Items {
						pods = append(pods, p.Name)
					}
					foundPods = true
					break
				}
			}
			if !foundPods {
				labelSelector = "" // Clear the label selector to trigger the default pod listing
			}
		} else if controllerType == "CRD" {
			// 其它自定义资源，优先用常见label查找同组pod
			podObj, _ := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			found := false
			for _, key := range []string{"app", "name", "component"} {
				if v, ok := podObj.Labels[key]; ok {
					labelSelector = fmt.Sprintf("%s=%s", key, v)
					podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
						LabelSelector: labelSelector,
					})
					if err == nil && len(podList.Items) > 0 {
						for _, p := range podList.Items {
							pods = append(pods, p.Name)
						}
						found = true
						break
					}
				}
			}
			if !found {
				pods = []string{podName}
			}
		}
		if labelSelector != "" && len(pods) == 0 {
			fmt.Printf("Searching for pods with label selector: %s in namespace: %s\n", labelSelector, namespace)
			podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				return fmt.Errorf("error listing pods: %v", err)
			}
			if len(podList.Items) == 0 {
				// 如果找不到pod，尝试从当前命名空间重新获取pod列表
				podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					return fmt.Errorf("error listing pods: %v", err)
				}
				if len(podList.Items) == 0 {
					return fmt.Errorf("no pods found in namespace %s", namespace)
				}
				fmt.Printf("No pods found for controller %s. Available pods:\n", controllerName)
				for _, p := range podList.Items {
					fmt.Printf("- %s\n", p.Name)
				}
				return fmt.Errorf("please select a valid pod from the list above")
			}
			for _, p := range podList.Items {
				pods = append(pods, p.Name)
			}
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
			var logContent strings.Builder
			for {
				n, err := stream.Read(buf)
				if err != nil {
					break
				}
				// 将新日志追加到已有日志末尾，并确保每行自动换行
				newLog := string(buf[:n])
				lines := strings.Split(newLog, "\n")
				for i, line := range lines {
					if i > 0 {
						logContent.WriteString("\n")
					}
					logContent.WriteString(line)
				}
				// 限制最大行数，避免日志过多导致翻滚过快
				allLines := strings.Split(logContent.String(), "\n")
				if len(allLines) > 1000 {
					allLines = allLines[len(allLines)-1000:]
					logContent.Reset()
					logContent.WriteString(strings.Join(allLines, "\n"))
				}
				logWidgets[index].Text = logContent.String()
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
