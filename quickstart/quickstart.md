# Agentic Networking Quickstart

Welcome! This guide provides a hands-on walkthrough for getting started with the Kube Agentic Networking project. In just a few steps, you'll learn how to deploy an AI agent to your Kubernetes cluster and use declarative, high-level policies to control its access to various tools.

## Overview

The goal of this quickstart is to demonstrate how to use the Agentic Networking APIs to enforce fine-grained authorization policies on an AI agent. The agent will be running in your Kubernetes cluster and will attempt to access tools exposed by two different [Model Context Protocol (MCP)](https://modelcontextprotocol.io/docs/getting-started/intro) servers:

1.  **An in-cluster MCP server**: An instance of the [`everything` reference server](https://github.com/modelcontextprotocol/servers/tree/main/src/everything), running inside your cluster.
2.  **A remote MCP server**: The public [`DeepWiki` server](https://docs.devin.ai/work-with-devin/deepwiki-mcp), hosted externally.

You will define `AuthPolicy` resources to specify which tools the agent is permitted to use from each server and observe how the Envoy proxy, configured by the Agentic Networking controller, enforces these rules.

## 1. Prerequisites

Before you begin, ensure you have the following tools installed and configured:

- **A Kubernetes cluster**: You can use a local cluster like `kind` or `minikube`, or a cloud-based one.
  > **Note**
  > If your cluster doesn't have a native `LoadBalancer` implementation (common in local setups), we recommend installing one like [MetalLB](https://metallb.universe.tf/installation/) to expose the agent's web UI.
- **`kubectl`**: The Kubernetes command-line tool. See the [official installation guide](https://kubernetes.io/docs/tasks/tools/#kubectl).
- **A configured `kubectl` context**: Your `kubectl` should be pointing to the cluster you intend to use.
  ```shell
  kubectl config use-context <YOUR-CLUSTER-NAME>
  ```
- **Go**: The Go programming language toolchain, required to run the controller.
- **A local clone of this repository**:
  ```shell
  git clone https://github.com/kubernetes-sigs/kube-agentic-networking.git
  cd kube-agentic-networking
  ```

## 2. Set Up the Kubernetes Environment

First, let's install the necessary Custom Resource Definitions (CRDs) and the in-cluster MCP server.

### Step 2.1: Install Gateway API CRDs

Agentic Networking builds upon the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/). Install the standard CRDs with the following command ([official guide](https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api)):

```shell
kubectl apply --server-side -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml
```

### Step 2.2: Install Agentic Networking CRDs

Next, install the `AuthPolicy` and `Backend` CRDs specific to this project:

```shell
kubectl apply -f k8s/crds/agentic.networking.x-k8s.io_authpolicies.yaml
kubectl apply -f k8s/crds/agentic.networking.x-k8s.io_backends.yaml
```

### Step 2.3: Deploy the In-Cluster MCP Server

Deploy the `everything` MCP reference server, which will act as the local tool provider for our agent.

```shell
kubectl apply -f quickstart/mcpserver/deployment.yaml
```

## 3. Define and Apply Network Policies

Now, we'll define the core networking resources that describe our desired agent behavior. The [quickstart/policy/e2e.yaml](/quickstart/policy/e2e.yaml) file contains all the necessary resources:

- **Gateway**: Defines the entry point for traffic, listening on port `10001`.
- **Backend**: Two `Backend` resources define the connection details for our local and remote MCP servers.
- **HTTPRoute**: Two `HTTPRoute` resources map URL paths (`/local/mcp` and `/remote/mcp`) to their respective `Backend`.
- **AuthPolicy**: Two `AuthPolicy` resources define the access rules. They specify that the agent (`adk-agent-sa` service account) is only allowed to use the `add` and `getTinyImage` tools from the local server, and the `read_wiki_structure` tool from the remote server.

Apply these resources to your cluster:

```shell
kubectl apply -f quickstart/policy/e2e.yaml
```

## 4. Deploy the Envoy Proxy

With the policies defined, it's time to run the Agentic Networking controller. This program will:

1.  Read the `Gateway`, `HTTPRoute`, `Backend`, and `AuthPolicy` resources you just created.
2.  Translate them into a corresponding Envoy proxy configuration.
3.  Deploy an Envoy instance to the cluster, configured to enforce your policies.

Run the controller from the root of the repository:

```shell
go run ./main.go --gateway agentic-net-gateway --namespace default
```

You will see a success message indicating that the Envoy configuration has been generated and the deployment is being rolled out.

```
✅ Envoy is ready! 🎉 You can access it within the cluster via one of the following methods:
```

## 5. Deploy the AI Agent

The final piece is the AI agent itself. We'll use a sample agent built with the [Agent Development Kit (ADK)](https://google.github.io/adk-docs/).

### Step 5.1: Configure LLM Authentication

The agent requires an API key to communicate with a Large Language Model (LLM). This guide uses the Google Gemini family of models.

> **Note**
> The agent is configurable and supports various LLM providers like Google, OpenAI and Anthropic. You can modify the [agent deployment manifest](/quickstart/adk-agent/deployment.yaml) to use a different provider by configuring the API key as an environment variable. This [ADK documentation site](https://google.github.io/adk-docs/agents/models/) covers the setup details.

1.  Obtain an API key from [Google AI Studio](https://aistudio.google.com/).
2.  Create a Kubernetes secret to securely store your key:
    ```shell
    kubectl create secret generic google-secret --from-literal=google-api-key='<PASTE-YOUR-API-KEY-HERE>'
    ```

### Step 5.2: Deploy the Agent

Deploy the agent's `Deployment` and `Service`:

```shell
kubectl apply -f quickstart/adk-agent/deployment.yaml
```

Wait for the deployment to complete and the agent to be ready:

```shell
kubectl wait --timeout=5m -n default deployment/adk-agent --for=condition=Available
```

## 6. Interact with the Agent

You can now interact with your agent through its web UI.

### Step 6.1: Access the Agent UI

Choose one of the following methods to access the UI:

- **Option A: Load Balancer (Cloud Clusters)**
  If your cluster has a load balancer, get the agent's external IP address:

  ```shell
  kubectl get svc adk-agent-svc -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
  ```

  Open this IP address in your web browser.

- **Option B: Port Forwarding (Local Clusters)**
  If you're running a local cluster, use `kubectl port-forward`:
  ```shell
  kubectl port-forward service/adk-agent-svc 8081:80 &
  ```
  Then, navigate to `http://localhost:8081` in your browser.

### Step 6.2: Chat with the Agent

In the agent UI, select `mcp_agent` from the dropdown menu in the top-left corner. You can now send prompts to the agent.

Try the following prompts and observe the results. The outcomes are determined by the `AuthPolicy` you deployed earlier.

| Prompt                                                           | Tool Invoked                        | Expected Result | Why?                                                                          |
| :--------------------------------------------------------------- | :---------------------------------- | :-------------- | :---------------------------------------------------------------------------- |
| "What can you do for me?"                                        | `tools/list` on both MCPs           | ✅ **Success**  | The default policy allows any user to list available tools.                   |
| "Can you do 2+3?"                                                | `add` on local MCP                  | ✅ **Success**  | The `AuthPolicy` for the local backend explicitly allows the `add` tool.      |
| "Can you echo back 'hello'?"                                     | `echo` on local MCP                 | ❌ **Failure**  | The `echo` tool is not in the allowlist for the local backend's `AuthPolicy`. |
| "Read the structure of the `modelcontextprotocol/servers` repo." | `read_wiki_structure` on remote MCP | ✅ **Success**  | The `AuthPolicy` for the remote backend explicitly allows this tool.          |
| "Read the wiki content of that repo."                            | `read_wiki_content` on remote MCP   | ❌ **Failure**  | The `read_wiki_content` tool is not in the allowlist for the remote backend.  |

> **Note**
> The agent currently returns a combined list of tools from both MCP servers, which includes tools not permitted by the configured `AuthPolicy`. Filtering disallowed tools from `tools/list` responses is a work in progress.

## 7. Recap

Congratulations! You have successfully:

- Installed the Agentic Networking CRDs.
- Defined declarative authorization policies for an AI agent.
- Run a controller that automatically configures and deploys an Envoy proxy to enforce those policies.
- Observed how the agent's access to tools is controlled at the network level based on your policies.

## 8. Clean Up

To remove all the resources created during this quickstart, run the following commands:

```shell
# Delete the agent, policies, and MCP server
kubectl delete -f quickstart/adk-agent/deployment.yaml
kubectl delete secret google-secret
kubectl delete -f quickstart/policy/e2e.yaml
kubectl delete -f quickstart/mcpserver/deployment.yaml

# Delete the Envoy deployment and its namespace
kubectl delete namespace agentic-net

# Uninstall Agentic Networking CRDs
kubectl delete -f k8s/crds/agentic.networking.x-k8s.io_authpolicies.yaml
kubectl delete -f k8s/crds/agentic.networking.x-k8s.io_backends.yaml

# Uninstall Gateway API CRDs
kubectl delete -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml
```
