// Queries behind the built-in pages. Each returns one series per host, labelled host_name.
// system_cpu_time is summed over CPUs, so busy share is 1 - idle / all.
export const HOST_CPU = `1 - sum by (host_name) (rate(system_cpu_time_seconds_total{state="idle"}[5m])) / sum by (host_name) (rate(system_cpu_time_seconds_total[5m]))`;
export const HOST_MEM = `sum by (host_name) (system_memory_usage_bytes{state="used"}) / sum by (host_name) (system_memory_usage_bytes{state=~"used|free|buffered|cached"})`;
export const HOST_LABEL = "host_name";

// Live usage per pod on a node, from cAdvisor (container="" is the pod's own cgroup, counted already).
export const podCPU = (node: string) =>
  `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{node="${node}", container!=""}[5m]))`;
export const podMemory = (node: string) =>
  `sum by (namespace, pod) (container_memory_working_set_bytes{node="${node}", container!=""})`;
