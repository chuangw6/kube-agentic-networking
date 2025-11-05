package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"time"

	envoyproxytypes "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"google.golang.org/protobuf/encoding/protojson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions"
	agenticclient "sigs.k8s.io/kube-agentic-networking/k8s/client/clientset/versioned"
	agenticinformers "sigs.k8s.io/kube-agentic-networking/k8s/client/informers/externalversions"
	"sigs.k8s.io/kube-agentic-networking/pkg/translator"
	"sigs.k8s.io/yaml"
)

// Constants for the Envoy deployment.
const (
	envoyDeploymentYAMLPath = "envoy/deployment.yaml" // Path to the base Envoy deployment manifest.
	envoyNamespace          = "agentic-net"           // The namespace where Envoy will be deployed.
	envoyDeploymentName     = "envoy-deployment"      // The name of the Envoy deployment.
	envoyServiceName        = "envoy-service"         // The name of the Envoy service.
)

var (
	gatewayName    = flag.String("gateway", "", "Name of the Gateway resource")
	gatewayNs      = flag.String("namespace", "default", "Namespace of the Gateway resource")
	outputJSONFile = flag.String("output-json", "envoy-xds.json", "Output file for the Envoy XDS configuration")
	outputYAMLFile = flag.String("output-yaml", "envoy-xds.yaml", "Output file for the Envoy XDS configuration in YAML format")
)

func main() {
	flag.Parse()

	if *gatewayName == "" || *gatewayNs == "" {
		fmt.Println("Error: --gateway and --namespace are required")
		os.Exit(1)
	}

	// Initialize Kubernetes clients
	usr, err := user.Current()
	if err != nil {
		fmt.Printf("Failed to get current user: %v\n", err)
		os.Exit(1)
	}
	kubeconfig := filepath.Join(usr.HomeDir, ".kube", "config")
	// Build Kubernetes config
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error building kubeconfig: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 1. Fetch Gateway resource
	gw, err := fetchGateway(ctx, config, *gatewayNs, *gatewayName)
	if err != nil {
		fmt.Printf("Error fetching gateway: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Fetched Gateway: %s/%s\n", gw.Namespace, gw.Name)

	// 2. Construct translator
	// The stop channel should be managed in the scope where the informers are used.
	stopCh := make(chan struct{})
	defer close(stopCh)
	translator, err := constructTranslator(config, stopCh)
	if err != nil {
		fmt.Printf("Error constructing translator: %v\n", err)

		os.Exit(1)
	}

	// 3. Translate Gateway to XDS resources
	resources, err := translator.TranslateGatewayToXDS(ctx, gw)
	if err != nil {
		fmt.Printf("Error translating Gateway to XDS: %v\n", err)
		os.Exit(1)
	}

	// 4. Generate static bootstrap config and save to files
	err = generateAndSaveBootstrapConfig(resources, *outputJSONFile, *outputYAMLFile)
	if err != nil {
		fmt.Printf("Error generating and saving bootstrap config: %v\n", err)
		os.Exit(1)
	}

	// 5. Deploy Envoy with the generated configuration
	if err := deployEnvoy(*outputYAMLFile); err != nil {
		fmt.Printf("Error deploying Envoy: %v\n", err)
		os.Exit(1)
	}
}

// fetchGateway retrieves the specified Gateway resource.
func fetchGateway(ctx context.Context, config *rest.Config, namespace, name string) (*gatewayv1.Gateway, error) {
	gatewayClientset, err := gatewayclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating Gateway API clientset: %w", err)
	}

	gw, err := gatewayClientset.GatewayV1().Gateways(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error fetching Gateway %s/%s: %w", namespace, name, err)
	}

	return gw, nil
}

// constructTranslator initializes and returns a new translator instance.
func constructTranslator(config *rest.Config, stopCh <-chan struct{}) (*translator.Translator, error) {
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating kube client: %w", err)
	}

	gatewayClientset, err := gatewayclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating Gateway API clientset: %w", err)
	}

	agenticClientset, err := agenticclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating Agentic clientset: %w", err)
	}

	sharedInformers := informers.NewSharedInformerFactory(kubeClient, 60*time.Second)
	sharedGwInformers := gatewayinformers.NewSharedInformerFactory(gatewayClientset, 60*time.Second)
	sharedAgenticInformers := agenticinformers.NewSharedInformerFactory(agenticClientset, 60*time.Second)

	hasSynced := []k8scache.InformerSynced{
		sharedInformers.Core().V1().Namespaces().Informer().HasSynced,
		sharedInformers.Core().V1().Services().Informer().HasSynced,
		sharedInformers.Core().V1().Secrets().Informer().HasSynced,
		sharedGwInformers.Gateway().V1().Gateways().Informer().HasSynced,
		sharedGwInformers.Gateway().V1().HTTPRoutes().Informer().HasSynced,
		sharedGwInformers.Gateway().V1beta1().ReferenceGrants().Informer().HasSynced,
		sharedAgenticInformers.Agentic().V1alpha1().AuthPolicies().Informer().HasSynced,
		sharedAgenticInformers.Agentic().V1alpha1().Backends().Informer().HasSynced,
	}

	go sharedGwInformers.Start(stopCh)
	go sharedInformers.Start(stopCh)
	go sharedAgenticInformers.Start(stopCh)
	k8scache.WaitForNamedCacheSync("test", stopCh, hasSynced...)

	// Get JWT issuer link from Kubernetes API server.
	jwtIssuer, err := translator.GetK8sIssuer(config)
	if err != nil {
		return nil, fmt.Errorf("error getting JWT issuer: %w", err)
	}

	return translator.New(
		jwtIssuer,
		kubeClient,
		gatewayClientset,
		sharedInformers.Core().V1().Namespaces().Lister(),
		sharedInformers.Core().V1().Services().Lister(),
		sharedInformers.Core().V1().Secrets().Lister(),
		sharedGwInformers.Gateway().V1().Gateways().Lister(),
		sharedGwInformers.Gateway().V1().HTTPRoutes().Lister(),
		sharedGwInformers.Gateway().V1beta1().ReferenceGrants().Lister(),
		sharedAgenticInformers.Agentic().V1alpha1().AuthPolicies().Lister(),
		sharedAgenticInformers.Agentic().V1alpha1().Backends().Lister(),
	), nil
}

// generateAndSaveBootstrapConfig creates the Envoy static bootstrap configuration and saves it to JSON and YAML files.
func generateAndSaveBootstrapConfig(resources map[resourcev3.Type][]envoyproxytypes.Resource, outputJSONFile, outputYAMLFile string) error {
	// To create a static bootstrap config, we need a structure like:
	// { "static_resources": { "listeners": [...], "clusters": [...] } }
	// We will marshal each resource individually and then place them into this structure.
	staticResources := make(map[string]interface{})
	marshaledResources := make(map[string][]json.RawMessage)

	for typeURL, resList := range resources {
		marshaler := protojson.MarshalOptions{
			UseProtoNames:   true, // Use snake_case names as seen in Envoy docs
			EmitUnpopulated: false,
		}

		for _, res := range resList {
			jsonBytes, err := marshaler.Marshal(res)
			if err != nil {
				return fmt.Errorf("error marshaling resource of type %s: %w", typeURL, err)
			}
			marshaledResources[typeURL] = append(marshaledResources[typeURL], jsonBytes)
		}
	}

	// Populate the static_resources map with the marshaled listeners and clusters.
	if listeners, ok := marshaledResources[resourcev3.ListenerType]; ok {
		staticResources["listeners"] = listeners
	}
	if clusters, ok := marshaledResources[resourcev3.ClusterType]; ok {
		staticResources["clusters"] = clusters
	}

	// Add the admin interface configuration. This provides a local endpoint for stats, etc.
	adminConfig := map[string]interface{}{
		"address": map[string]interface{}{
			"socket_address": map[string]interface{}{
				"address":    "0.0.0.0",
				"port_value": 9901, // Default Envoy admin port
			},
		},
	}

	// The final output structure that Envoy expects for a static config.
	output := map[string]interface{}{
		"admin":            adminConfig,
		"static_resources": staticResources,
	}
	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling final output to JSON: %v\n", err)
		return err
	}

	if err := os.WriteFile(outputJSONFile, jsonBytes, 0644); err != nil {
		return fmt.Errorf("error writing to output file %s: %w", outputJSONFile, err)
	}
	fmt.Printf("Successfully wrote XDS to %s\n", outputJSONFile)

	yamlBytes, err := yaml.JSONToYAML(jsonBytes)
	if err != nil {
		return fmt.Errorf("error converting JSON to YAML: %w", err)
	}

	if err := os.WriteFile(outputYAMLFile, yamlBytes, 0644); err != nil {
		return fmt.Errorf("error writing to output file %s: %w", outputYAMLFile, err)
	}
	fmt.Printf("Successfully wrote XDS to %s\n", outputYAMLFile)
	return nil
}

// deployEnvoy applies the Envoy deployment and configuration to the cluster.
func deployEnvoy(bootstrapConfigFilename string) error {
	fmt.Printf("\nApplying Envoy deployment from %s with generated config %s...\n", envoyDeploymentYAMLPath, bootstrapConfigFilename)

	// Step 1: Ensure the namespace exists. This is an idempotent command.
	fmt.Printf("Ensuring namespace '%s' exists...\n", envoyNamespace)
	cmdCreateNS := fmt.Sprintf("kubectl create namespace %s --dry-run=client -o yaml | kubectl apply -f -", envoyNamespace)
	cmd1 := exec.Command("sh", "-c", cmdCreateNS)
	cmd1.Stdout = os.Stdout
	cmd1.Stderr = os.Stderr
	if err := cmd1.Run(); err != nil {
		return fmt.Errorf("failed to ensure namespace exists: %w", err)
	}

	// Step 2: Create or update the ConfigMap using the generated envoy-xds.yaml file.
	// We use --dry-run and pipe to `kubectl apply` to make this operation idempotent.
	// This will create the configmap if it doesn't exist, or update it if it does.
	fmt.Printf("Creating/updating envoy-config ConfigMap in namespace '%s'...\n", envoyNamespace)
	cmdCreateCM := fmt.Sprintf("kubectl create configmap envoy-config --from-file=envoy.yaml=%s -n %s -o yaml --dry-run=client | kubectl apply -f -", bootstrapConfigFilename, envoyNamespace)
	cmd2 := exec.Command("sh", "-c", cmdCreateCM)
	cmd2.Stdout = os.Stdout
	cmd2.Stderr = os.Stderr
	if err := cmd2.Run(); err != nil {
		return fmt.Errorf("failed to apply configmap: %w", err)
	}

	// Step 3: Apply the rest of the deployment resources, which now have the namespace defined internally.
	fmt.Printf("Applying deployment resources from %s...\n", envoyDeploymentYAMLPath)
	cmd3 := exec.Command("kubectl", "apply", "-f", envoyDeploymentYAMLPath)
	cmd3.Stdout = os.Stdout
	cmd3.Stderr = os.Stderr
	if err := cmd3.Run(); err != nil {
		return fmt.Errorf("failed to apply deployment yaml: %w", err)
	}

	// Step 4: Wait for the deployment to be available.
	fmt.Printf("Waiting for %s to become available in namespace '%s'...\n", envoyDeploymentName, envoyNamespace)
	cmdWait := fmt.Sprintf("kubectl wait --timeout=5m -n %s deployment/%s --for=condition=Available", envoyNamespace, envoyDeploymentName)
	cmd4 := exec.Command("sh", "-c", cmdWait)
	cmd4.Stdout = os.Stdout
	cmd4.Stderr = os.Stderr
	if err := cmd4.Run(); err != nil {
		return fmt.Errorf("failed while waiting for deployment to become available: %w", err)
	}

	// Step 5: Get and print the service ClusterIP.
	fmt.Printf("Fetching ClusterIP and port for envoy-service in namespace '%s'...\n", envoyNamespace)
	cmdGetIP := fmt.Sprintf("kubectl get service %s -n %s -o jsonpath='{.spec.clusterIP}'", envoyServiceName, envoyNamespace)
	cmd5 := exec.Command("sh", "-c", cmdGetIP)
	clusterIP, err := cmd5.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get service ClusterIP: %w\nOutput: %s", err, string(clusterIP))
	}

	cmdGetPort := fmt.Sprintf("kubectl get service %s -n %s -o jsonpath='{.spec.ports[0].port}'", envoyServiceName, envoyNamespace)
	cmd6 := exec.Command("sh", "-c", cmdGetPort)
	port, err := cmd6.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get service port: %w\nOutput: %s", err, string(port))
	}

	// Print the final success message.
	fmt.Println("\n-----------------------------------------------------------------")
	fmt.Println("✅ Envoy is ready! 🎉 You can access it within the cluster via one of the following methods:")
	fmt.Printf("- Cluster IP: %s:%s\n", clusterIP, port)
	fmt.Printf("- FQDN: %s.%s.svc.cluster.local:%s\n", envoyServiceName, envoyNamespace, port)
	fmt.Println("-----------------------------------------------------------------")
	return nil
}
