package worker

import (
	"fmt"

	"github.com/golang/glog"
	v1 "k8s.io/api/core/v1"

	"github.com/turbonomic/kubeturbo/pkg/discovery/metrics"
	"github.com/turbonomic/kubeturbo/pkg/discovery/repository"
	"github.com/turbonomic/kubeturbo/pkg/discovery/task"
	"github.com/turbonomic/kubeturbo/pkg/discovery/util"
)

// Collects parent info for the pods and containers and converts to EntityGroup objects
type GroupMetricsCollector struct {
	PodList     []*v1.Pod
	MetricsSink *metrics.EntityMetricSink
	workerId    string
}

func NewGroupMetricsCollector(discoveryWorker *k8sDiscoveryWorker, currTask *task.Task) *GroupMetricsCollector {
	metricsCollector := &GroupMetricsCollector{
		PodList:     currTask.PodList(),
		MetricsSink: discoveryWorker.sink,
		workerId:    discoveryWorker.id,
	}
	return metricsCollector
}

func (collector *GroupMetricsCollector) CollectGroupMetrics() []*repository.EntityGroup {
	var entityGroupList []*repository.EntityGroup

	entityGroups := make(map[string]*repository.EntityGroup)
	entityGroupsByParentKind := make(map[string]*repository.EntityGroup)

	for _, pod := range collector.PodList {
		podKey := util.PodKeyFunc(pod)
		ownerTypeString, ownerString, err := collector.getGroupName(metrics.PodType, podKey)
		if err != nil {
			// TODO: Handle this informational logging in a better way
			// collector.getGroupName can return a bool in place of error
			// as its not really an error.
			glog.V(4).Infof(err.Error())
			continue
		}

		podId := string(pod.UID)
		groupKey := fmt.Sprintf("%s/%s/%s", ownerTypeString, pod.Namespace, ownerString)

		// group1 = A group for each parent qualified as namespace/parentName of this kind/type
		if _, groupExists := entityGroups[groupKey]; !groupExists {
			entityGroups[groupKey] = repository.NewEntityGroup(ownerTypeString, ownerString, groupKey)
			entityGroupList = append(entityGroupList, entityGroups[groupKey])
		}
		entityGroup := entityGroups[groupKey]

		// group2 = One global group by each parent kind/type
		if _, exists := entityGroupsByParentKind[ownerTypeString]; !exists {
			entityGroupsByParentKind[ownerTypeString] = repository.NewEntityGroup(ownerTypeString, "", ownerTypeString)
			entityGroupList = append(entityGroupList, entityGroupsByParentKind[ownerTypeString])
		}
		entityGroupByParentKind := entityGroupsByParentKind[ownerTypeString]

		// We currently skip adding pod members which results in no groups of
		// pods per parent controller for each namespace being created. These groups
		// are not needed and cause performance overhead, especially in large topologies.
		//
		// TODO: Cleanup all the relevant group creation code after the alternative
		// mechanism for consistent scaling of containers across pods of one parent
		// is in place.
		// TODO: Also consider having code for atleast some of these autocreated groups,
		// behind a flag useful for smaller topologies.
		//
		// entityGroup.AddMember(metrics.PodType, podId)

		// Add pod member to the group2
		entityGroupByParentKind.AddMember(metrics.PodType, podId)

		for i := range pod.Spec.Containers {
			// Add container members to the group
			containerId := util.ContainerIdFunc(podId, i)
			entityGroup.AddMember(metrics.ContainerType, containerId)
			entityGroupByParentKind.AddMember(metrics.ContainerType, containerId)

			// Compute groups for different containers in the pod
			container := pod.Spec.Containers[i]
			containerName := container.Name

			// Add subgroups of containers by name as members to group1 only
			// (Sub-groups that are to be created with consistent resize = true).
			if _, containerGroupExists := entityGroup.ContainerGroups[containerName]; !containerGroupExists {
				entityGroup.ContainerGroups[containerName] = []string{}
			}
			entityGroup.ContainerGroups[containerName] = append(entityGroup.ContainerGroups[containerName], containerId)
		}
	}

	return entityGroupList
}

func (collector *GroupMetricsCollector) getGroupName(etype metrics.DiscoveredEntityType, entityKey string) (string, string, error) {
	ownerTypeMetricId := metrics.GenerateEntityStateMetricUID(etype, entityKey, metrics.OwnerType)
	ownerMetricId := metrics.GenerateEntityStateMetricUID(etype, entityKey, metrics.Owner)

	ownerTypeMetric, err := collector.MetricsSink.GetMetric(ownerTypeMetricId)
	if err != nil {
		return "", "", fmt.Errorf("Error getting owner type for pod %s --> %v\n", entityKey, err)
	}
	ownerType := ownerTypeMetric.GetValue()
	ownerTypeString, ok := ownerType.(string)
	if !ok || ownerTypeString == "" {
		return "", "", fmt.Errorf("Empty owner type for pod %s\n", entityKey)
	}

	ownerMetric, err := collector.MetricsSink.GetMetric(ownerMetricId)
	if err != nil {
		return "", "", fmt.Errorf("Error getting owner for pod %s --> %v\n", entityKey, err)
	}

	owner := ownerMetric.GetValue()
	ownerString, ok := owner.(string)
	if !ok || ownerString == "" {
		return "", "", fmt.Errorf("Empty owner for pod %s\n", entityKey)
	}

	return ownerTypeString, ownerString, nil
}
