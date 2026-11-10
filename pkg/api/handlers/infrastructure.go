package handlers

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/mcp"
	"github.com/obot-platform/obot/pkg/obotlogs"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/substrate"
	pb "github.com/obot-platform/obot/pkg/substrate/ateapipb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// InfrastructureHandler shows administrators the Kubernetes workloads Obot provisions (MCP
// servers, Claude environment workers and Obot itself) and Obot's own recent logs.
type InfrastructureHandler struct {
	engine        string
	kube          kclient.Client
	clientset     kubernetes.Interface
	mcpNamespace  string
	obotNamespace string
	substrate     *substrate.Client
	logs          *obotlogs.Buffer
}

func NewInfrastructureHandler(engine string, kube kclient.Client, config *rest.Config, mcpNamespace, obotNamespace string, sub *substrate.Client, logs *obotlogs.Buffer) *InfrastructureHandler {
	h := &InfrastructureHandler{
		engine:        engine,
		mcpNamespace:  mcpNamespace,
		obotNamespace: obotNamespace,
		substrate:     sub,
		logs:          logs,
	}
	if mcp.IsKubernetesBackend(engine) && kube != nil {
		h.kube = kube
		if config != nil {
			if cs, err := kubernetes.NewForConfig(config); err == nil {
				h.clientset = cs
			}
		}
	}
	return h
}

// InfraEvent is a Kubernetes event about a workload, its pods or its replica sets.
type InfraEvent struct {
	Type     string    `json:"type"`
	Reason   string    `json:"reason"`
	Message  string    `json:"message"`
	Count    int32     `json:"count,omitempty"`
	Object   string    `json:"object"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
}

// InfraPod is one pod's state.
type InfraPod struct {
	Name            string         `json:"name"`
	Phase           string         `json:"phase"`
	Status          string         `json:"status"`
	Ready           string         `json:"ready"`
	Restarts        int32          `json:"restarts"`
	LastTermination string         `json:"lastTermination,omitempty"`
	Message         string         `json:"message,omitempty"`
	Node            string         `json:"node,omitempty"`
	Created         time.Time      `json:"created"`
	Images          []string       `json:"images,omitempty"`
	Sessions        []InfraSession `json:"sessions,omitempty"`
}

// InfraSession is a Claude environment session running on a worker.
type InfraSession struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	UserID   string    `json:"userID,omitempty"`
	UserName string    `json:"userName,omitempty"`
	State    string    `json:"state,omitempty"`
	Created  time.Time `json:"created"`
}

// InfraResources is a container's requests and limits.
type InfraResources struct {
	CPURequest    string `json:"cpuRequest,omitempty"`
	MemoryRequest string `json:"memoryRequest,omitempty"`
	CPULimit      string `json:"cpuLimit,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty"`
}

// InfraWorkload is a deployment and its pods.
type InfraWorkload struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	DisplayName   string `json:"displayName,omitempty"`
	Kind          string `json:"kind"`
	Health        string `json:"health"`
	HealthReason  string `json:"healthReason,omitempty"`
	Replicas      int32  `json:"replicas"`
	ReadyReplicas int32  `json:"readyReplicas"`
	Restarts      int32  `json:"restarts"`
	Image         string `json:"image,omitempty"`
	InfraResources
	Created time.Time `json:"created"`

	MCPServerID          string `json:"mcpServerID,omitempty"`
	CatalogEntryID       string `json:"catalogEntryID,omitempty"`
	CatalogEntryName     string `json:"catalogEntryName,omitempty"`
	VMCPID               string `json:"vmcpID,omitempty"`
	VMCPName             string `json:"vmcpName,omitempty"`
	PowerUserWorkspaceID string `json:"powerUserWorkspaceID,omitempty"`
	UserID               string `json:"userID,omitempty"`
	UserName             string `json:"userName,omitempty"`
	EnvironmentID        string `json:"environmentID,omitempty"`
	EnvironmentName      string `json:"environmentName,omitempty"`
	// Link is the Obot UI path for what this workload runs.
	Link string `json:"link,omitempty"`

	WarningEvents int          `json:"warningEvents"`
	Pods          []InfraPod   `json:"pods"`
	Events        []InfraEvent `json:"events"`
}

// InfraProblem is something an administrator should look at.
type InfraProblem struct {
	Severity  string `json:"severity"`
	Area      string `json:"area"`
	Name      string `json:"name"`
	Workload  string `json:"workload,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Message   string `json:"message"`
	Link      string `json:"link,omitempty"`
}

// InfraSection is one group of workloads.
type InfraSection struct {
	Namespace string          `json:"namespace,omitempty"`
	Enabled   bool            `json:"enabled"`
	Error     string          `json:"error,omitempty"`
	Workloads []InfraWorkload `json:"workloads"`
}

// Infrastructure is the summary GET /api/infrastructure returns.
type Infrastructure struct {
	Engine      string         `json:"engine"`
	Kubernetes  bool           `json:"kubernetes"`
	Message     string         `json:"message,omitempty"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Problems    []InfraProblem `json:"problems"`
	Obot        InfraSection   `json:"obot"`
	MCPServers  InfraSection   `json:"mcpServers"`
	Claude      InfraSection   `json:"claude"`
}

// Get returns the summary of every workload, problems first.
func (h *InfrastructureHandler) Get(req api.Context) error {
	out := Infrastructure{
		Engine:      h.engine,
		Kubernetes:  h.kube != nil,
		GeneratedAt: time.Now().UTC(),
		Problems:    []InfraProblem{},
		Obot:        InfraSection{Workloads: []InfraWorkload{}},
		MCPServers:  InfraSection{Workloads: []InfraWorkload{}},
		Claude:      InfraSection{Workloads: []InfraWorkload{}},
	}
	if h.kube == nil {
		out.Message = fmt.Sprintf("Obot runs MCP servers on the %s engine. Workload details need Obot to run its MCP servers on Kubernetes. Obot's own logs are still available.", cmp.Or(h.engine, "docker"))
		return req.Write(out)
	}

	users := newUserNames(req)
	out.Obot = h.obotSection(req.Context())
	out.MCPServers = h.mcpSection(req, users)
	out.Claude = h.claudeSection(req, users)

	for area, section := range map[string]*InfraSection{"obot": &out.Obot, "mcp": &out.MCPServers, "claude": &out.Claude} {
		if section.Error != "" {
			out.Problems = append(out.Problems, InfraProblem{Severity: "warning", Area: area, Name: section.Namespace, Namespace: section.Namespace, Message: section.Error})
		}
		for _, w := range section.Workloads {
			out.Problems = append(out.Problems, workloadProblems(area, w)...)
		}
	}
	sort.SliceStable(out.Problems, func(i, j int) bool {
		if out.Problems[i].Severity != out.Problems[j].Severity {
			return out.Problems[i].Severity == "error"
		}
		return out.Problems[i].Area < out.Problems[j].Area
	})
	return req.Write(out)
}

// GetMCPServer returns one MCP server workload with all its pods and events. The name is the
// deployment's, which is the MCP server's (or vMCP's) ID.
func (h *InfrastructureHandler) GetMCPServer(req api.Context) error {
	if h.kube == nil {
		return types.NewErrHTTP(http.StatusNotImplemented, "MCP server workloads are only available when Obot runs MCP servers on Kubernetes")
	}
	name := req.PathValue("name")
	section := h.workloads(req.Context(), h.mcpNamespace, func(d *appsv1.Deployment) bool { return d.Name == name }, 0)
	if section.Error != "" {
		return types.NewErrHTTP(http.StatusBadGateway, section.Error)
	}
	if len(section.Workloads) == 0 {
		return types.NewErrNotFound("no deployment %q in namespace %s: the server may be stopped, remote, or not deployed yet", name, h.mcpNamespace)
	}
	w := section.Workloads[0]
	h.describeMCP(req, newUserNames(req), &w)
	return req.Write(w)
}

// MCPServerLogs returns recent log lines from an MCP server's pod.
func (h *InfrastructureHandler) MCPServerLogs(req api.Context) error {
	if h.kube == nil || h.clientset == nil {
		return types.NewErrHTTP(http.StatusNotImplemented, "MCP server logs are only available here when Obot runs MCP servers on Kubernetes")
	}
	name := req.PathValue("name")
	var deployment appsv1.Deployment
	if err := h.kube.Get(req.Context(), kclient.ObjectKey{Namespace: h.mcpNamespace, Name: name}, &deployment); err != nil {
		return types.NewErrNotFound("no deployment %q in namespace %s", name, h.mcpNamespace)
	}
	var pods corev1.PodList
	if err := h.kube.List(req.Context(), &pods, kclient.InNamespace(h.mcpNamespace), kclient.MatchingLabels(deployment.Spec.Selector.MatchLabels)); err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return types.NewErrNotFound("deployment %q has no pods", name)
	}
	// Prefer the pod asked for, else the newest one.
	pod := &pods.Items[0]
	for i := range pods.Items {
		p := &pods.Items[i]
		if want := req.URL.Query().Get("pod"); want != "" {
			if p.Name == want {
				pod = p
				break
			}
			continue
		}
		if p.CreationTimestamp.After(pod.CreationTimestamp.Time) {
			pod = p
		}
	}
	container := req.URL.Query().Get("container")
	if container == "" && len(pod.Spec.Containers) > 0 {
		container = pod.Spec.Containers[0].Name
		for _, c := range pod.Spec.Containers {
			if c.Name == "mcp" {
				container = c.Name
			}
		}
	}
	tail := int64(infraQueryInt(req, "tail", 200, 2000))
	previous := req.URL.Query().Get("previous") == "true"
	stream, err := h.clientset.CoreV1().Pods(h.mcpNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container:  container,
		TailLines:  &tail,
		Timestamps: true,
		Previous:   previous,
	}).Stream(req.Context())
	if err != nil {
		return types.NewErrHTTP(http.StatusBadGateway, fmt.Sprintf("couldn't read logs of pod %s: %v", pod.Name, err))
	}
	defer stream.Close()
	body, err := io.ReadAll(io.LimitReader(stream, 2<<20))
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	if q := strings.Fields(strings.ToLower(req.URL.Query().Get("query"))); len(q) > 0 {
		lines = slices.DeleteFunc(lines, func(l string) bool {
			l = strings.ToLower(l)
			for _, t := range q {
				if !strings.Contains(l, t) {
					return true
				}
			}
			return false
		})
	}
	return req.Write(map[string]any{
		"deployment": name,
		"pod":        pod.Name,
		"container":  container,
		"previous":   previous,
		"lines":      lines,
	})
}

// Logs searches Obot's own recent output.
func (h *InfrastructureHandler) Logs(req api.Context) error {
	q := obotlogs.Query{
		Text:     req.URL.Query().Get("query"),
		MinLevel: req.URL.Query().Get("level"),
		Limit:    infraQueryInt(req, "limit", 200, 2000),
	}
	if since := strings.TrimSpace(req.URL.Query().Get("since")); since != "" {
		if d, err := time.ParseDuration(since); err == nil {
			q.Since = time.Now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.Since = t
		} else {
			return types.NewErrBadRequest("since must be a duration such as 15m or an RFC 3339 time")
		}
	}
	result := h.logs.Search(q)
	return req.Write(map[string]any{
		"pod":      os.Getenv("HOSTNAME"),
		"lines":    result.Lines,
		"matched":  result.Matched,
		"buffered": result.Buffered,
		"capacity": result.Capacity,
		"oldestAt": result.OldestAt,
		"note":     "Only this Obot replica's output since it started, up to the buffer's capacity.",
	})
}

func infraQueryInt(req api.Context, key string, def, maximum int) int {
	n, err := strconv.Atoi(req.URL.Query().Get(key))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, maximum)
}

// obotSection finds the deployment this Obot pod belongs to.
func (h *InfrastructureHandler) obotSection(ctx context.Context) InfraSection {
	section := InfraSection{Namespace: h.obotNamespace, Enabled: true, Workloads: []InfraWorkload{}}
	if h.obotNamespace == "" {
		section.Error = "Obot doesn't know which namespace it runs in"
		return section
	}
	var deploymentName string
	hostname, _ := os.Hostname()
	var pod corev1.Pod
	if err := h.kube.Get(ctx, kclient.ObjectKey{Namespace: h.obotNamespace, Name: hostname}, &pod); err == nil {
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == "ReplicaSet" {
				var rs appsv1.ReplicaSet
				if err := h.kube.Get(ctx, kclient.ObjectKey{Namespace: h.obotNamespace, Name: ref.Name}, &rs); err == nil {
					for _, owner := range rs.OwnerReferences {
						if owner.Kind == "Deployment" {
							deploymentName = owner.Name
						}
					}
				}
			}
		}
	}
	if deploymentName == "" {
		// Running outside the cluster, as in development: nothing of Obot's own to show.
		section.Error = fmt.Sprintf("couldn't find this Obot server's pod (%s) in namespace %s; it may be running outside the cluster", hostname, h.obotNamespace)
		return section
	}
	found := h.workloads(ctx, h.obotNamespace, func(d *appsv1.Deployment) bool { return d.Name == deploymentName }, 10)
	found.Enabled = true
	for i := range found.Workloads {
		found.Workloads[i].Kind = "obot"
		found.Workloads[i].DisplayName = "Obot"
	}
	return found
}

func (h *InfrastructureHandler) mcpSection(req api.Context, users *userNames) InfraSection {
	section := h.workloads(req.Context(), h.mcpNamespace, nil, 5)
	section.Enabled = true
	for i := range section.Workloads {
		h.describeMCP(req, users, &section.Workloads[i])
	}
	return section
}

// describeMCP links a deployment back to the Obot object it runs.
func (h *InfrastructureHandler) describeMCP(req api.Context, users *userNames, w *InfraWorkload) {
	w.Kind = "mcpServer"
	var server v1.MCPServer
	if err := req.Get(&server, w.Name); err == nil {
		w.MCPServerID = server.Name
		w.DisplayName = cmp.Or(server.Spec.Alias, server.Spec.Manifest.Name, w.DisplayName)
		w.CatalogEntryID = server.Spec.MCPServerCatalogEntryName
		w.PowerUserWorkspaceID = server.Spec.PowerUserWorkspaceID
		w.VMCPID = server.Spec.VMCPID
		w.UserID = cmp.Or(server.Spec.UserID, w.UserID)
		if w.CatalogEntryID != "" {
			var entry v1.MCPServerCatalogEntry
			if err := req.Get(&entry, w.CatalogEntryID); err == nil {
				w.CatalogEntryName = entry.Spec.Manifest.Name
			}
		}
		if w.VMCPID == "" && server.Spec.VMCPInstanceID != "" {
			var instance v1.VMCPInstance
			if err := req.Get(&instance, server.Spec.VMCPInstanceID); err == nil {
				w.VMCPID = instance.Spec.Manifest.VMCPID
			}
		}
		switch {
		case w.VMCPID != "":
			w.Link = "/vmcps/" + w.VMCPID
		case w.CatalogEntryID != "" && w.PowerUserWorkspaceID != "":
			w.Link = "/mcp-servers/c/" + w.CatalogEntryID + "?wid=" + w.PowerUserWorkspaceID
		case w.CatalogEntryID != "":
			w.Link = "/mcp-servers/c/" + w.CatalogEntryID
		case w.PowerUserWorkspaceID != "":
			w.Link = "/mcp-servers/s/" + w.Name + "?wid=" + w.PowerUserWorkspaceID
		default:
			w.Link = "/mcp-servers/s/" + w.Name
		}
	} else {
		var vmcp v1.VMCP
		var system v1.SystemMCPServer
		switch {
		case req.Get(&vmcp, w.Name) == nil:
			w.Kind = "vmcp"
			w.VMCPID = vmcp.Name
			w.DisplayName = cmp.Or(vmcp.Spec.Manifest.DisplayName, w.DisplayName)
			w.Link = "/vmcps/" + vmcp.Name
		case req.Get(&system, w.Name) == nil:
			w.Kind = "system"
			w.DisplayName = cmp.Or(system.Spec.Manifest.Name, w.DisplayName)
		}
	}
	if w.VMCPID != "" {
		var vmcp v1.VMCP
		if err := req.Get(&vmcp, w.VMCPID); err == nil {
			w.VMCPName = vmcp.Spec.Manifest.DisplayName
		}
	}
	w.UserName = users.name(w.UserID)
	w.DisplayName = cmp.Or(w.DisplayName, w.Name)
}

func (h *InfrastructureHandler) claudeSection(req api.Context, users *userNames) InfraSection {
	if h.substrate == nil {
		return InfraSection{Workloads: []InfraWorkload{}}
	}
	ctx := req.Context()
	namespace := h.substrate.Config.Namespace
	section := h.workloads(ctx, namespace, nil, 5)
	section.Enabled = true

	names := map[string]string{}
	if environments, err := req.GatewayClient.ClaudeEnvironments(ctx); err == nil {
		for _, env := range environments {
			names[env.ID] = env.DisplayName
		}
	}
	// Worker pools whose deployment hasn't appeared yet still matter.
	pools := &unstructured.UnstructuredList{}
	pools.SetAPIVersion("ate.dev/v1alpha1")
	pools.SetKind("WorkerPoolList")
	if err := h.kube.List(ctx, pools, kclient.InNamespace(namespace)); err == nil {
		for _, pool := range pools.Items {
			if !slices.ContainsFunc(section.Workloads, func(w InfraWorkload) bool { return w.Name == pool.GetName() }) {
				replicas, _, _ := unstructured.NestedInt64(pool.Object, "spec", "replicas")
				ready, _, _ := unstructured.NestedInt64(pool.Object, "status", "readyReplicas")
				w := InfraWorkload{Name: pool.GetName(), Namespace: namespace, Replicas: int32(replicas), ReadyReplicas: int32(ready), Created: pool.GetCreationTimestamp().Time, Pods: []InfraPod{}, Events: []InfraEvent{}}
				w.Health, w.HealthReason = "starting", "The worker pool has no deployment yet"
				section.Workloads = append(section.Workloads, w)
			}
		}
	}

	// Sessions: which ones each worker runs, when Substrate says; otherwise the environment's
	// running sessions.
	var instances v1.AgentInstanceList
	_ = req.List(&instances)
	byName := map[string]*v1.AgentInstance{}
	for i := range instances.Items {
		byName[instances.Items[i].Name] = &instances.Items[i]
	}
	assigned := h.workerAssignments(ctx)
	session := func(instance *v1.AgentInstance) InfraSession {
		return InfraSession{
			ID:       agentSessionID(instance),
			Name:     instance.Spec.DisplayName,
			UserID:   instance.Spec.UserID,
			UserName: users.name(instance.Spec.UserID),
			State:    instance.Status.State,
			Created:  instance.CreationTimestamp.Time,
		}
	}
	for i := range section.Workloads {
		w := &section.Workloads[i]
		w.Kind = "claudeWorkerPool"
		w.EnvironmentID = w.Name
		w.EnvironmentName = names[w.Name]
		w.DisplayName = cmp.Or(w.EnvironmentName, w.Name)
		w.Link = "/claude-environments"
		for j := range w.Pods {
			pod := &w.Pods[j]
			if actors, ok := assigned[pod.Name]; ok {
				for _, actor := range actors {
					if instance := byName[actor]; instance != nil {
						pod.Sessions = append(pod.Sessions, session(instance))
					}
				}
			}
		}
		if assigned == nil {
			// Without Substrate's assignments, show the environment's running sessions on the
			// pool's first pod so they're still visible.
			var running []InfraSession
			for _, instance := range instances.Items {
				if instance.Spec.ClaudeEnvironment == w.Name && instance.Status.State == "RUNNING" && !instance.Spec.ClaudeDone {
					running = append(running, session(&instance))
				}
			}
			if len(running) > 0 && len(w.Pods) == 1 {
				w.Pods[0].Sessions = running
			}
		}
	}
	return section
}

// workerAssignments maps worker pod names to the actors (agent instance names) Substrate has
// assigned to them. Nil when Substrate can't say.
func (h *InfrastructureHandler) workerAssignments(ctx context.Context) map[string][]string {
	if h.substrate == nil || h.substrate.Control == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	workers, err := h.substrate.Control.ListWorkers(ctx, &pb.ListWorkersRequest{PageSize: 1000})
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, worker := range workers.GetWorkers() {
		if worker.GetWorkerNamespace() != h.substrate.Config.Namespace {
			continue
		}
		assignments, err := h.substrate.Control.ListWorkerActorAssignments(ctx, &pb.ListWorkerActorAssignmentsRequest{
			Worker:   &pb.ObjectRef{Name: worker.GetMetadata().GetName()},
			PageSize: 1000,
		})
		if err != nil {
			return nil
		}
		for _, a := range assignments.GetActorAssignments() {
			out[worker.GetWorkerPod()] = append(out[worker.GetWorkerPod()], a.GetActor().GetName())
		}
	}
	return out
}

// workloads summarizes the deployments in namespace that keep selects (all when nil), with
// up to maxEvents events each (all when 0).
func (h *InfrastructureHandler) workloads(ctx context.Context, namespace string, keep func(*appsv1.Deployment) bool, maxEvents int) InfraSection {
	section := InfraSection{Namespace: namespace, Workloads: []InfraWorkload{}}
	var deployments appsv1.DeploymentList
	if err := h.kube.List(ctx, &deployments, kclient.InNamespace(namespace)); err != nil {
		section.Error = fmt.Sprintf("couldn't list deployments in %s: %v", namespace, err)
		return section
	}
	var pods corev1.PodList
	if err := h.kube.List(ctx, &pods, kclient.InNamespace(namespace)); err != nil {
		section.Error = fmt.Sprintf("couldn't list pods in %s (Obot may need RBAC to read them): %v", namespace, err)
	}
	var events corev1.EventList
	if err := h.kube.List(ctx, &events, kclient.InNamespace(namespace)); err != nil && section.Error == "" {
		section.Error = fmt.Sprintf("couldn't list events in %s (Obot may need RBAC to read them): %v", namespace, err)
	}

	for i := range deployments.Items {
		d := &deployments.Items[i]
		if keep != nil && !keep(d) {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
		if err != nil {
			continue
		}
		w := InfraWorkload{
			Name:          d.Name,
			Namespace:     d.Namespace,
			DisplayName:   d.Annotations["mcp-server-display-name"],
			UserID:        d.Labels["mcp-user-id"],
			ReadyReplicas: d.Status.ReadyReplicas,
			Created:       d.CreationTimestamp.Time,
			Pods:          []InfraPod{},
			Events:        []InfraEvent{},
		}
		if d.Spec.Replicas != nil {
			w.Replicas = *d.Spec.Replicas
		}
		if containers := d.Spec.Template.Spec.Containers; len(containers) > 0 {
			c := containers[0]
			for _, candidate := range containers {
				if candidate.Name == "mcp" {
					c = candidate
				}
			}
			w.Image = c.Image
			w.InfraResources = resourcesOf(c.Resources)
		}
		podNames := map[string]bool{}
		for _, pod := range pods.Items {
			if selector.Matches(labels.Set(pod.Labels)) {
				p := podView(&pod)
				w.Pods = append(w.Pods, p)
				w.Restarts += p.Restarts
				podNames[pod.Name] = true
			}
		}
		sort.Slice(w.Pods, func(i, j int) bool { return w.Pods[i].Created.After(w.Pods[j].Created) })
		for _, e := range events.Items {
			obj := e.InvolvedObject
			if (obj.Kind == "Deployment" && obj.Name == d.Name) ||
				(obj.Kind == "ReplicaSet" && strings.HasPrefix(obj.Name, d.Name+"-")) ||
				(obj.Kind == "Pod" && podNames[obj.Name]) {
				w.Events = append(w.Events, eventView(&e))
				if e.Type == corev1.EventTypeWarning {
					w.WarningEvents++
				}
			}
		}
		sortEvents(w.Events)
		if maxEvents > 0 && len(w.Events) > maxEvents {
			w.Events = w.Events[:maxEvents]
		}
		w.Health, w.HealthReason = workloadHealth(d, w.Pods)
		section.Workloads = append(section.Workloads, w)
	}
	sort.SliceStable(section.Workloads, func(i, j int) bool {
		ri, rj := healthRank(section.Workloads[i].Health), healthRank(section.Workloads[j].Health)
		if ri != rj {
			return ri < rj
		}
		return section.Workloads[i].Created.After(section.Workloads[j].Created)
	})
	return section
}

func resourcesOf(r corev1.ResourceRequirements) InfraResources {
	var out InfraResources
	if q, ok := r.Requests[corev1.ResourceCPU]; ok {
		out.CPURequest = q.String()
	}
	if q, ok := r.Requests[corev1.ResourceMemory]; ok {
		out.MemoryRequest = q.String()
	}
	if q, ok := r.Limits[corev1.ResourceCPU]; ok {
		out.CPULimit = q.String()
	}
	if q, ok := r.Limits[corev1.ResourceMemory]; ok {
		out.MemoryLimit = q.String()
	}
	return out
}

// failingReasons are container states that won't fix themselves.
var failingReasons = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
	"OOMKilled":                  true,
	"Error":                      true,
	"Unschedulable":              true,
}

func podView(pod *corev1.Pod) InfraPod {
	p := InfraPod{
		Name:    pod.Name,
		Phase:   string(pod.Status.Phase),
		Status:  string(pod.Status.Phase),
		Node:    pod.Spec.NodeName,
		Created: pod.CreationTimestamp.Time,
		Message: pod.Status.Message,
	}
	ready := 0
	for _, c := range pod.Spec.Containers {
		p.Images = append(p.Images, c.Image)
	}
	for _, c := range pod.Status.InitContainerStatuses {
		if c.State.Waiting != nil && c.State.Waiting.Reason != "" && c.State.Waiting.Reason != "PodInitializing" {
			p.Status = "Init:" + c.State.Waiting.Reason
			p.Message = cmp.Or(c.State.Waiting.Message, p.Message)
		}
	}
	for _, c := range pod.Status.ContainerStatuses {
		if c.Ready {
			ready++
		}
		p.Restarts += c.RestartCount
		if t := c.LastTerminationState.Terminated; t != nil {
			p.LastTermination = fmt.Sprintf("%s (exit %d) %s", t.Reason, t.ExitCode, t.FinishedAt.UTC().Format(time.RFC3339))
		}
		switch {
		case c.State.Waiting != nil && c.State.Waiting.Reason != "":
			p.Status = c.State.Waiting.Reason
			p.Message = cmp.Or(c.State.Waiting.Message, p.Message)
		case c.State.Terminated != nil && c.State.Terminated.Reason != "":
			p.Status = c.State.Terminated.Reason
			p.Message = cmp.Or(c.State.Terminated.Message, p.Message)
		}
	}
	p.Ready = fmt.Sprintf("%d/%d", ready, len(pod.Spec.Containers))
	if pod.Status.Phase == corev1.PodPending {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason != "" {
				p.Status = cond.Reason
				p.Message = cmp.Or(cond.Message, p.Message)
			}
		}
	}
	if pod.DeletionTimestamp != nil {
		p.Status = "Terminating"
	}
	return p
}

func workloadHealth(d *appsv1.Deployment, pods []InfraPod) (string, string) {
	if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 {
		return "stopped", "Scaled to zero"
	}
	for _, p := range pods {
		if failingReasons[p.Status] {
			reason := fmt.Sprintf("Pod %s is %s", p.Name, p.Status)
			if p.Message != "" {
				reason += ": " + p.Message
			}
			if p.LastTermination != "" {
				reason += "; last termination: " + p.LastTermination
			}
			return "failing", reason
		}
	}
	for _, c := range d.Status.Conditions {
		if (c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse) ||
			(c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue) {
			return "failing", cmp.Or(c.Message, c.Reason)
		}
	}
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	if d.Status.ReadyReplicas >= desired {
		return "healthy", ""
	}
	for _, p := range pods {
		if p.Status != "Running" {
			return "starting", fmt.Sprintf("Pod %s is %s", p.Name, p.Status)
		}
	}
	return "starting", fmt.Sprintf("%d of %d replicas ready", d.Status.ReadyReplicas, desired)
}

func healthRank(health string) int {
	switch health {
	case "failing":
		return 0
	case "starting":
		return 1
	case "healthy":
		return 2
	}
	return 3
}

func eventView(e *corev1.Event) InfraEvent {
	last := e.LastTimestamp.Time
	if last.IsZero() {
		last = e.EventTime.Time
	}
	if last.IsZero() {
		last = e.CreationTimestamp.Time
	}
	return InfraEvent{
		Type:     e.Type,
		Reason:   e.Reason,
		Message:  e.Message,
		Count:    e.Count,
		Object:   strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name,
		LastSeen: last,
	}
}

// sortEvents puts warnings first, newest first within each type.
func sortEvents(events []InfraEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		wi, wj := events[i].Type == corev1.EventTypeWarning, events[j].Type == corev1.EventTypeWarning
		if wi != wj {
			return wi
		}
		return events[i].LastSeen.After(events[j].LastSeen)
	})
}

func workloadProblems(area string, w InfraWorkload) []InfraProblem {
	var out []InfraProblem
	add := func(severity, message string) {
		out = append(out, InfraProblem{Severity: severity, Area: area, Name: w.DisplayName, Workload: w.Name, Namespace: w.Namespace, Message: message, Link: w.Link})
	}
	switch w.Health {
	case "failing":
		add("error", w.HealthReason)
	case "starting":
		if time.Since(w.Created) > 5*time.Minute {
			add("warning", "Not ready: "+w.HealthReason)
		}
	}
	if w.Health != "failing" && w.Restarts > 0 {
		var last string
		for _, p := range w.Pods {
			if p.LastTermination != "" {
				last = "; last termination: " + p.LastTermination
				break
			}
		}
		add("warning", fmt.Sprintf("Restarted %d times%s", w.Restarts, last))
	}
	if w.Health == "healthy" {
		if ev, ok := ongoingWarning(w); ok {
			add("warning", fmt.Sprintf("Recent warning: %s: %s", ev.Reason, ev.Message))
		}
	}
	return out
}

// ongoingWarning is the newest warning on a healthy workload that is still worth a look: it
// came after the workload's pods finished starting, about a pod that still exists. Warnings
// from a pod's startup, such as a volume not mounted yet, resolve themselves once it's ready.
func ongoingWarning(w InfraWorkload) (InfraEvent, bool) {
	pods := map[string]time.Time{}
	for _, p := range w.Pods {
		pods["pod/"+p.Name] = p.Created
	}
	for _, ev := range w.Events {
		if ev.Type != corev1.EventTypeWarning || time.Since(ev.LastSeen) > 30*time.Minute {
			continue
		}
		if created, ok := pods[ev.Object]; strings.HasPrefix(ev.Object, "pod/") && (!ok || ev.LastSeen.Before(created.Add(2*time.Minute))) {
			continue
		}
		return ev, true
	}
	return InfraEvent{}, false
}

// userNames looks up and remembers users' names for one request.
type userNames struct {
	req   api.Context
	names map[string]string
}

func newUserNames(req api.Context) *userNames {
	return &userNames{req: req, names: map[string]string{}}
}

func (u *userNames) name(id string) string {
	if id == "" {
		return ""
	}
	if name, ok := u.names[id]; ok {
		return name
	}
	var name string
	if user, err := u.req.GatewayClient.UserByIDIncludeDeleted(u.req.Context(), id); err == nil {
		name = cmp.Or(user.DisplayName, user.Email, user.Username)
	}
	u.names[id] = name
	return name
}
