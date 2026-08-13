package tools

const (
	PrometheusQuery = "prometheus.query"
	K8sPodList      = "k8s.pod.list"
	K8sPodLogs      = "k8s.pod.logs"
	HTTPProbe       = "http.probe"
	SystemVerify    = "system.verify"
	AlertCorrelate  = "alert.correlate"
	KnowledgeSearch = "knowledge.search"
)

func init() {
	registeredTools[PrometheusQuery] = Tool{Name: PrometheusQuery, Operation: Read, Risk: Low, Domain: "prometheus", ResourceType: "query"}
	registeredTools[K8sPodList] = Tool{Name: K8sPodList, Operation: Read, Risk: Low, Domain: "k8s", ResourceType: "pod"}
	registeredTools[K8sPodLogs] = Tool{Name: K8sPodLogs, Operation: Read, Risk: Low, Domain: "k8s", ResourceType: "pod"}
	registeredTools[HTTPProbe] = Tool{Name: HTTPProbe, Operation: Read, Risk: Low, Domain: "http", ResourceType: "endpoint"}
	registeredTools[SystemVerify] = Tool{Name: SystemVerify, Operation: Read, Risk: Low, Domain: "system", ResourceType: "verify"}
	registeredTools[AlertCorrelate] = Tool{Name: AlertCorrelate, Operation: Read, Risk: Low, Domain: "alert", ResourceType: "correlate"}
	registeredTools[KnowledgeSearch] = Tool{Name: KnowledgeSearch, Operation: Read, Risk: Low, Domain: "knowledge", ResourceType: "search"}
}
