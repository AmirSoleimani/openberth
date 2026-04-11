package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
	"github.com/AmirSoleimani/openberth/apps/server/internal/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	k8sNamespace  = "openberth"
	labelApp      = "openberth"
	labelDeployID = "openberth.deploy-id"
	labelPhase    = "openberth.phase"
	defaultServerPVC = "openberth-data" // shared PVC with the server for code access
)

// K8sManager implements Manager using Kubernetes Pods and Services.
type K8sManager struct {
	cfg            *config.Config
	client         kubernetes.Interface
	restCfg        *rest.Config
	namespace      string
	serverPVC      string // PVC name shared with the server for code access
	gvisorRuntime  *string // non-nil if gVisor RuntimeClass exists
}

// NewK8sManager creates a K8s-backed container manager.
// It tries in-cluster config first, then falls back to kubeconfig.
func NewK8sManager(cfg *config.Config) (*K8sManager, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("k8s config: %w", err)
		}
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	ns := os.Getenv("OPENBERTH_K8S_NAMESPACE")
	if ns == "" {
		ns = k8sNamespace
	}

	pvcName := os.Getenv("OPENBERTH_SERVER_PVC")
	if pvcName == "" {
		pvcName = defaultServerPVC
	}

	km := &K8sManager{
		cfg:       cfg,
		client:    client,
		restCfg:   restCfg,
		namespace: ns,
		serverPVC: pvcName,
	}

	if err := km.ensureNamespace(); err != nil {
		return nil, fmt.Errorf("ensure namespace: %w", err)
	}

	// Check if gVisor RuntimeClass exists in the cluster
	km.gvisorRuntime = km.detectGVisorRuntime()
	if km.gvisorRuntime != nil {
		log.Printf("[k8s] gVisor RuntimeClass detected: %s", *km.gvisorRuntime)
	}

	return km, nil
}

// detectGVisorRuntime checks if a gVisor RuntimeClass exists.
// Looks for "gvisor" or "runsc" RuntimeClass names.
func (km *K8sManager) detectGVisorRuntime() *string {
	ctx := context.Background()
	for _, name := range []string{"gvisor", "runsc"} {
		_, err := km.client.NodeV1().RuntimeClasses().Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return &name
		}
	}
	return nil
}

func (km *K8sManager) ensureNamespace() error {
	ctx := context.Background()
	_, err := km.client.CoreV1().Namespaces().Get(ctx, km.namespace, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: km.namespace},
		}
		if _, err := km.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (km *K8sManager) GVisorAvailable() bool {
	return km.gvisorRuntime != nil
}

// applyGVisor sets the RuntimeClassName on the pod spec if gVisor is available.
func (km *K8sManager) applyGVisor(spec *corev1.PodSpec) {
	if km.gvisorRuntime != nil {
		spec.RuntimeClassName = km.gvisorRuntime
	}
}

func (km *K8sManager) podName(deployID string) string {
	return "ob-" + deployID
}

func (km *K8sManager) svcName(deployID string) string {
	return "ob-" + deployID
}

func (km *K8sManager) envVars(opts CreateOpts) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "PORT", Value: fmt.Sprintf("%d", opts.Port)},
		{Name: "DATA_DIR", Value: "/data"},
	}
	for k, v := range opts.FrameworkEnv {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	for k, v := range opts.UserEnv {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	return env
}

// parseDockerMemory converts Docker-style memory strings to K8s resource.Quantity.
// Docker accepts: "512m", "1g", "256mb", "1gb", plain bytes "536870912".
// K8s expects: "512Mi", "1Gi", or plain bytes.
func parseDockerMemory(mem string) (resource.Quantity, error) {
	mem = strings.TrimSpace(strings.ToLower(mem))
	if mem == "" {
		return resource.MustParse("512Mi"), nil
	}

	// Try plain number (bytes)
	if _, err := strconv.ParseInt(mem, 10, 64); err == nil {
		return resource.MustParse(mem), nil
	}

	// Strip trailing "b" if present (e.g., "512mb" → "512m")
	mem = strings.TrimSuffix(mem, "b")

	if strings.HasSuffix(mem, "g") {
		val := strings.TrimSuffix(mem, "g")
		return resource.MustParse(val + "Gi"), nil
	}
	if strings.HasSuffix(mem, "m") {
		val := strings.TrimSuffix(mem, "m")
		return resource.MustParse(val + "Mi"), nil
	}
	if strings.HasSuffix(mem, "k") {
		val := strings.TrimSuffix(mem, "k")
		return resource.MustParse(val + "Ki"), nil
	}

	return resource.ParseQuantity(mem)
}

func (km *K8sManager) resourceRequirements(memory, cpus string) corev1.ResourceRequirements {
	if memory == "" {
		memory = km.cfg.Container.Memory
	}
	if cpus == "" {
		cpus = km.cfg.Container.CPUs
	}

	memQty, err := parseDockerMemory(memory)
	if err != nil {
		log.Printf("[k8s] Warning: invalid memory %q, defaulting to 512Mi: %v", memory, err)
		memQty = resource.MustParse("512Mi")
	}

	cpuQty, err := resource.ParseQuantity(cpus)
	if err != nil {
		log.Printf("[k8s] Warning: invalid cpu %q, defaulting to 0.5: %v", cpus, err)
		cpuQty = resource.MustParse("500m")
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: memQty,
			corev1.ResourceCPU:    cpuQty,
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: memQty,
			corev1.ResourceCPU:    cpuQty,
		},
	}
}

// pvcName returns the PVC name for a deployment's workspace or data volume.
func (km *K8sManager) pvcName(deployID, suffix string) string {
	return "ob-" + deployID + "-" + suffix
}

// codeVolume returns a volume and mount that accesses the deploy's code from
// the shared server PVC. The server stores code at {dataDir}/deploys/{id}/
// which maps to subPath "deploys/{id}" in the PVC.
func (km *K8sManager) codeVolume(deployID, mountPath string, readOnly bool) (corev1.Volume, corev1.VolumeMount) {
	subPath := "deploys/" + deployID
	vol := corev1.Volume{
		Name: "code",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: km.serverPVC,
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "code",
		MountPath: mountPath,
		SubPath:   subPath,
		ReadOnly:  readOnly,
	}
	return vol, mount
}

// ensurePVC creates a PVC if it doesn't already exist.
func (km *K8sManager) ensurePVC(name, size string) error {
	ctx := context.Background()
	_, err := km.client.CoreV1().PersistentVolumeClaims(km.namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil // already exists
	}
	if !errors.IsNotFound(err) {
		return err
	}

	storageQty, _ := parseDockerMemory(size)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: km.namespace,
			Labels:    map[string]string{labelApp: "true"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageQty,
				},
			},
		},
	}

	_, err = km.client.CoreV1().PersistentVolumeClaims(km.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	return err
}

// Create runs a two-phase deploy: init container (build) → main container (run).
func (km *K8sManager) Create(opts CreateOpts) (*ContainerResult, error) {
	p := framework.GetProvider(opts.Language)

	log.Printf("[k8s] Creating deployment %s (%s/%s)", opts.ID, opts.Language, opts.Framework)

	// Write build and run scripts to code dir
	if p != nil && !p.StaticOnly() {
		buildScript := p.BuildScript(fwInfoFromOpts(opts))
		if err := os.WriteFile(filepath.Join(opts.CodeDir, ".openberth-build.sh"), []byte(buildScript), 0755); err != nil {
			return nil, fmt.Errorf("write build script: %w", err)
		}
		runScript := p.RunScript(fwInfoFromOpts(opts))
		if err := os.WriteFile(filepath.Join(opts.CodeDir, ".openberth-run.sh"), []byte(runScript), 0755); err != nil {
			return nil, fmt.Errorf("write run script: %w", err)
		}
	}

	// Delete existing pod (keep PVCs and service)
	km.deletePod(opts.ID)

	podName := km.podName(opts.ID)

	// Ensure PVCs for workspace and data persistence
	wsPVC := km.pvcName(opts.ID, "ws")
	dataPVC := km.pvcName(opts.ID, "data")
	if err := km.ensurePVC(wsPVC, "2g"); err != nil {
		log.Printf("[k8s] Warning: failed to create workspace PVC: %v (falling back to EmptyDir)", err)
	}
	if err := km.ensurePVC(dataPVC, "1g"); err != nil {
		log.Printf("[k8s] Warning: failed to create data PVC: %v (falling back to EmptyDir)", err)
	}

	isStatic := p != nil && p.StaticOnly()
	port := opts.Port

	var pod *corev1.Pod

	if isStatic {
		// Static sites: mount code at /srv from shared PVC, serve with Caddy
		codeVol, codeMount := km.codeVolume(opts.ID, "/srv", true)
		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: km.namespace,
				Labels: map[string]string{
					labelApp:      "true",
					labelDeployID: opts.ID,
					labelPhase:    "run",
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyAlways,
				Containers: []corev1.Container{
					{
						Name:    "app",
						Image:   opts.Image,
						Command: []string{"caddy"},
						Args:    []string{"file-server", "--root", "/srv", "--listen", fmt.Sprintf(":%d", port)},
						Ports: []corev1.ContainerPort{
							{ContainerPort: int32(port), Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{codeMount},
						Resources:    km.resourceRequirements("128m", "0.25"),
					},
				},
				Volumes: []corev1.Volume{codeVol},
			},
		}
	} else {
		// Dynamic sites: init container builds, main container runs
		codeVol, codeMount := km.codeVolume(opts.ID, "/app/code", true)
		buildCodeMount := codeMount // same volume, same mount for init container

		initContainers := []corev1.Container{
			{
				Name:         "build",
				Image:        opts.Image,
				Command:      []string{"/bin/sh", "/app/code/.openberth-build.sh"},
				Env:          km.envVars(opts),
				VolumeMounts: []corev1.VolumeMount{buildCodeMount, {Name: "workspace", MountPath: "/app", SubPath: "app"}},
				Resources:    km.resourceRequirements("", ""),
			},
		}

		wsVolSrc := km.volumeSource(wsPVC)
		dataVolSrc := km.volumeSource(dataPVC)

		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: km.namespace,
				Labels: map[string]string{
					labelApp:      "true",
					labelDeployID: opts.ID,
					labelPhase:    "run",
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy:  corev1.RestartPolicyAlways,
				InitContainers: initContainers,
				Containers: []corev1.Container{
					{
						Name:    "app",
						Image:   opts.runtimeImage(),
						Command: []string{"/bin/sh", "/app/code/.openberth-run.sh"},
						Env:     km.envVars(opts),
						Ports: []corev1.ContainerPort{
							{ContainerPort: int32(port), Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							codeMount,
							{Name: "workspace", MountPath: "/app", SubPath: "app"},
							{Name: "data", MountPath: "/data"},
							{Name: "tmp", MountPath: "/tmp"},
						},
						Resources: km.resourceRequirements(opts.Memory, opts.CPUs),
					},
				},
				Volumes: []corev1.Volume{
					codeVol,
					{Name: "workspace", VolumeSource: wsVolSrc},
					{Name: "data", VolumeSource: dataVolSrc},
					{
						Name: "tmp",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								SizeLimit: resourcePtr(resource.MustParse("256Mi")),
							},
						},
					},
				},
			},
		}
	}

	km.applyGVisor(&pod.Spec)

	ctx := context.Background()
	created, err := km.client.CoreV1().Pods(km.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s create pod: %w", err)
	}

	// Create or update Service
	km.ensureService(opts.ID, port)

	// Wait for pod to be running
	if err := km.waitForPod(opts.ID, 5*time.Minute); err != nil {
		logs := km.Logs(opts.ID, 50)
		km.deletePod(opts.ID)
		return nil, fmt.Errorf("pod failed to start: %w\nLogs:\n%s", err, logs)
	}

	log.Printf("[k8s] Pod %s running", podName)

	hostPort := km.getServicePort(opts.ID)

	return &ContainerResult{
		ContainerID: string(created.UID),
		HostPort:    hostPort,
		Name:        podName,
		GVisor:      km.gvisorRuntime != nil,
	}, nil
}

// volumeSource returns a PVC volume source if the PVC exists, otherwise EmptyDir.
func (km *K8sManager) volumeSource(pvcName string) corev1.VolumeSource {
	ctx := context.Background()
	_, err := km.client.CoreV1().PersistentVolumeClaims(km.namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err == nil {
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}
	return corev1.VolumeSource{
		EmptyDir: &corev1.EmptyDirVolumeSource{},
	}
}

func (km *K8sManager) ensureService(deployID string, port int) {
	ctx := context.Background()
	svcName := km.svcName(deployID)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: km.namespace,
			Labels: map[string]string{
				labelApp:      "true",
				labelDeployID: deployID,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{labelDeployID: deployID},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt32(int32(port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	_, err := km.client.CoreV1().Services(km.namespace).Create(ctx, svc, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		if _, err := km.client.CoreV1().Services(km.namespace).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
			log.Printf("[k8s] Warning: failed to update service %s: %v", svcName, err)
		}
	} else if err != nil {
		log.Printf("[k8s] Warning: failed to create service %s: %v", svcName, err)
	}
}

func (km *K8sManager) waitForPod(deployID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	podName := km.podName(deployID)
	for {
		pod, err := km.client.CoreV1().Pods(km.namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("pod entered Failed state")
		case corev1.PodSucceeded:
			return fmt.Errorf("pod exited unexpectedly")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for pod %s", podName)
		case <-time.After(2 * time.Second):
		}
	}
}

func (km *K8sManager) getServicePort(deployID string) int {
	ctx := context.Background()
	svc, err := km.client.CoreV1().Services(km.namespace).Get(ctx, km.svcName(deployID), metav1.GetOptions{})
	if err != nil {
		return 0
	}
	if len(svc.Spec.Ports) > 0 {
		if svc.Spec.Ports[0].NodePort != 0 {
			return int(svc.Spec.Ports[0].NodePort)
		}
		return int(svc.Spec.Ports[0].TargetPort.IntVal)
	}
	return 0
}

// Rebuild deletes the pod and recreates it (blue-green equivalent).
// PVCs are preserved so build cache and data survive.
func (km *K8sManager) Rebuild(opts CreateOpts) (*ContainerResult, error) {
	log.Printf("[k8s] Rebuilding %s", opts.ID)
	return km.Create(opts)
}

// RecreateRuntime restarts the pod without rebuilding (for secret rotation).
// PVCs are preserved.
func (km *K8sManager) RecreateRuntime(opts CreateOpts) (*ContainerResult, error) {
	log.Printf("[k8s] Recreating runtime for %s", opts.ID)
	return km.Create(opts)
}

// CreateSandbox creates a pod with dev server configuration.
func (km *K8sManager) CreateSandbox(opts SandboxOpts) (*ContainerResult, error) {
	p := framework.GetProvider(opts.Language)

	log.Printf("[k8s] Creating sandbox %s (%s/%s)", opts.ID, opts.Language, opts.Framework)

	if p != nil && !p.StaticOnly() {
		entrypoint := p.SandboxEntrypoint(fwInfoFromSandboxOpts(opts), opts.Port)
		if err := os.WriteFile(filepath.Join(opts.CodeDir, ".openberth-sandbox.sh"), []byte(entrypoint), 0755); err != nil {
			return nil, fmt.Errorf("write sandbox entrypoint: %w", err)
		}
	}

	km.deletePod(opts.ID)

	podName := km.podName(opts.ID)
	var command []string

	if p != nil && p.StaticOnly() {
		command = []string{"caddy", "file-server", "--root", "/srv", "--listen", fmt.Sprintf(":%d", opts.Port)}
	} else {
		command = []string{"/bin/sh", "/app/.openberth-sandbox.sh"}
	}

	envVars := []corev1.EnvVar{
		{Name: "PORT", Value: fmt.Sprintf("%d", opts.Port)},
		{Name: "DATA_DIR", Value: "/data"},
		{Name: "NODE_ENV", Value: "development"},
		{Name: "CHOKIDAR_USEPOLLING", Value: "true"},
		{Name: "WATCHPACK_POLLING", Value: "true"},
	}
	for k, v := range opts.FrameworkEnv {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	if p != nil {
		for k, v := range p.SandboxEnv() {
			envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
		}
	}
	for k, v := range opts.UserEnv {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Sandbox mounts code from the shared server PVC
	sandboxMountPath := "/app"
	if p != nil && p.StaticOnly() {
		sandboxMountPath = "/srv"
	}
	sandboxCodeVol, sandboxCodeMount := km.codeVolume(opts.ID, sandboxMountPath, p != nil && p.StaticOnly())

	memory := opts.Memory
	if memory == "" {
		memory = "1g"
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: km.namespace,
			Labels: map[string]string{
				labelApp:      "true",
				labelDeployID: opts.ID,
				labelPhase:    "sandbox",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{
				{
					Name:    "app",
					Image:   opts.Image,
					Command: command,
					Env:     envVars,
					Ports: []corev1.ContainerPort{
						{ContainerPort: int32(opts.Port), Protocol: corev1.ProtocolTCP},
					},
					VolumeMounts: []corev1.VolumeMount{
						sandboxCodeMount,
						{Name: "data", MountPath: "/data"},
						{Name: "tmp", MountPath: "/tmp"},
					},
					Resources: km.resourceRequirements(memory, km.cfg.Container.CPUs),
				},
			},
			Volumes: []corev1.Volume{
				sandboxCodeVol,
				{
					Name: "data",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "tmp",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	km.applyGVisor(&pod.Spec)

	ctx := context.Background()
	created, err := km.client.CoreV1().Pods(km.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s create sandbox pod: %w", err)
	}

	km.ensureService(opts.ID, opts.Port)

	if err := km.waitForPod(opts.ID, 3*time.Minute); err != nil {
		logs := km.Logs(opts.ID, 50)
		km.deletePod(opts.ID)
		return nil, fmt.Errorf("sandbox pod failed to start: %w\nLogs:\n%s", err, logs)
	}

	hostPort := km.getServicePort(opts.ID)

	return &ContainerResult{
		ContainerID: string(created.UID),
		HostPort:    hostPort,
		Name:        podName,
		GVisor:      km.gvisorRuntime != nil,
	}, nil
}

var exitCodeRe = regexp.MustCompile(`exit code (\d+)`)

// ExecInContainer runs a command inside a running pod.
func (km *K8sManager) ExecInContainer(deployID string, command string, timeout time.Duration) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	podName := km.podName(deployID)
	req := km.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(km.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "app",
			Command:   []string{"sh", "-c", command},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(km.restCfg, "POST", req.URL())
	if err != nil {
		return "", 1, err
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	output := stdout.String() + stderr.String()
	exitCode := 0
	if err != nil {
		exitCode = 1
		// Try to extract exit code from K8s error message
		if m := exitCodeRe.FindStringSubmatch(err.Error()); len(m) == 2 {
			if code, e := strconv.Atoi(m[1]); e == nil {
				exitCode = code
			}
		}
	}
	return output, exitCode, err
}

// Destroy removes the pod, service, and PVCs for a deployment.
func (km *K8sManager) Destroy(deployID string) {
	ctx := context.Background()
	podName := km.podName(deployID)
	svcName := km.svcName(deployID)

	gracePeriod := int64(5)
	km.client.CoreV1().Pods(km.namespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	km.client.CoreV1().Services(km.namespace).Delete(ctx, svcName, metav1.DeleteOptions{})

	// Clean up PVCs
	for _, suffix := range []string{"ws", "data"} {
		pvcName := km.pvcName(deployID, suffix)
		km.client.CoreV1().PersistentVolumeClaims(km.namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	}
}

// deletePod removes just the pod (preserves service and PVCs).
func (km *K8sManager) deletePod(deployID string) {
	ctx := context.Background()
	gracePeriod := int64(5)
	km.client.CoreV1().Pods(km.namespace).Delete(ctx, km.podName(deployID), metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	// Wait for pod to actually terminate
	for i := 0; i < 15; i++ {
		_, err := km.client.CoreV1().Pods(km.namespace).Get(ctx, km.podName(deployID), metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// Logs returns the last N lines of pod logs.
func (km *K8sManager) Logs(deployID string, tail int) string {
	ctx := context.Background()
	podName := km.podName(deployID)
	tailLines := int64(tail)

	req := km.client.CoreV1().Pods(km.namespace).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &tailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Sprintf("Error fetching logs: %v", err)
	}
	defer stream.Close()

	buf := new(bytes.Buffer)
	io.Copy(buf, stream)
	return buf.String()
}

// LogStream returns a streaming reader of live pod logs.
func (km *K8sManager) LogStream(deployID string, tail int) (io.ReadCloser, error) {
	ctx := context.Background()
	podName := km.podName(deployID)
	tailLines := int64(tail)

	req := km.client.CoreV1().Pods(km.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		TailLines: &tailLines,
	})
	return req.Stream(ctx)
}

// Status returns the pod phase as a status string.
func (km *K8sManager) Status(deployID string) string {
	ctx := context.Background()
	podName := km.podName(deployID)

	pod, err := km.client.CoreV1().Pods(km.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "not_found"
	}

	switch pod.Status.Phase {
	case corev1.PodRunning:
		return "running"
	case corev1.PodPending:
		return "building"
	case corev1.PodFailed:
		return "failed"
	case corev1.PodSucceeded:
		return "stopped"
	default:
		return string(pod.Status.Phase)
	}
}

// Restart recreates the pod while preserving the service and PVCs.
// Unlike Docker's restart, bare K8s pods don't auto-reschedule, so we
// must read the current pod spec, delete, and recreate.
func (km *K8sManager) Restart(deployID string) bool {
	ctx := context.Background()
	podName := km.podName(deployID)

	// Read existing pod spec before deleting
	existing, err := km.client.CoreV1().Pods(km.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return false
	}

	// Save the spec and clear scheduler-set fields
	spec := existing.Spec.DeepCopy()
	spec.NodeName = ""

	// Delete the pod and wait
	km.deletePod(deployID)

	// Recreate with same spec
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: km.namespace,
			Labels:    existing.Labels,
		},
		Spec: *spec,
	}

	_, err = km.client.CoreV1().Pods(km.namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		log.Printf("[k8s] Failed to recreate pod %s: %v", podName, err)
		return false
	}

	return true
}

// InspectPort returns the target port from the deployment's service.
func (km *K8sManager) InspectPort(deployID string) int {
	return km.getServicePort(deployID)
}

func resourcePtr(r resource.Quantity) *resource.Quantity {
	return &r
}
