package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func infraDeployment(name string, replicas, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "obot-mcp", Labels: map[string]string{"mcp-user-id": "7"}, Annotations: map[string]string{"mcp-server-display-name": name + " display"}},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "mcp", Image: "img:" + name}}}},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func infraPod(name, app string, status corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "obot-mcp", Labels: map[string]string{"app": app}, CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "mcp"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{status}},
	}
}

func TestInfrastructureWorkloads(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(k8sscheme.Scheme).WithObjects(
		infraDeployment("ms1good", 1, 1),
		infraDeployment("ms1crash", 1, 0),
		infraDeployment("ms1idle", 0, 0),
		infraPod("ms1good-a", "ms1good", corev1.ContainerStatus{Name: "mcp", Ready: true, RestartCount: 2, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}}}),
		infraPod("ms1crash-a", "ms1crash", corev1.ContainerStatus{Name: "mcp", RestartCount: 5, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off restarting"}}}),
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "obot-mcp"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "ms1crash-a"},
			Type:           corev1.EventTypeWarning,
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			LastTimestamp:  metav1.Now(),
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e2", Namespace: "obot-mcp"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "ms1crash-a"},
			Type:           corev1.EventTypeNormal,
			Reason:         "Pulled",
			LastTimestamp:  metav1.Now(),
		},
	).Build()
	h := &InfrastructureHandler{kube: kube}
	section := h.workloads(context.Background(), "obot-mcp", nil, 0)
	if section.Error != "" {
		t.Fatalf("error: %s", section.Error)
	}

	tests := []struct {
		name     string
		health   string
		restarts int32
		events   int
		problem  string
	}{
		{
			name:     "ms1crash",
			health:   "failing",
			restarts: 5,
			events:   2,
			problem:  "CrashLoopBackOff",
		},
		{
			name:     "ms1good",
			health:   "healthy",
			restarts: 2,
			problem:  "OOMKilled",
		},
		{
			name:   "ms1idle",
			health: "stopped",
		},
	}
	if len(section.Workloads) != len(tests) {
		t.Fatalf("got %d workloads, want %d", len(section.Workloads), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := section.Workloads[i]
			if w.Name != tt.name || w.Health != tt.health || w.Restarts != tt.restarts || len(w.Events) != tt.events {
				t.Fatalf("got %s health=%s restarts=%d events=%d (%s)", w.Name, w.Health, w.Restarts, len(w.Events), w.HealthReason)
			}
			if w.Image != "img:"+tt.name || w.UserID != "7" {
				t.Errorf("image %q, user %q", w.Image, w.UserID)
			}
			if tt.events > 0 && w.Events[0].Type != corev1.EventTypeWarning {
				t.Error("warnings should come first")
			}
			problems := workloadProblems("mcp", w)
			if tt.problem == "" {
				if len(problems) != 0 {
					t.Errorf("unexpected problems %+v", problems)
				}
				return
			}
			if len(problems) == 0 || !strings.Contains(problems[0].Message, tt.problem) {
				t.Errorf("problems %+v, want one mentioning %s", problems, tt.problem)
			}
		})
	}
}
