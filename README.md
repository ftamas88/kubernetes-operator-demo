# Kubernetes operator demo

### Installation prerequisites:

- Kubernetes cluster (local or remote)
- Kubebuilder v3 installed (version 3.0.0 or higher)
- Docker installed and configured
- Go programming language installed (version 1.16 or higher)

## Initialise

1. Create a new directory for your operator:
```shell
$ mkdir nginx-operator-demo
$ cd nginx-operator-demo
```

2. Initialize the Kubebuilder project:
```shell
$ export GO111MODULE=on
$ export GOPROXY=https://proxy.golang.org,direct
$ kubebuilder init --domain kube-operator.local
```

3. Create the custom API:
```shell
$ kubebuilder create api --group apps --version v1 --image=nginx:latest --kind App --plugins=deploy-image.go.kubebuilder.io/alpha
```

4. Customise files in `api/v1/custom_types.go`


5. Customise controller in `internal/controller/custom_controller.go`


6. Install the CRDs in the cluster.
```shell
make install
```
or
```shell
kubectl apply -f config/crd/bases/apps.kubeoperator.local_apps.yaml
```

7. Configure your application at `config/samples/custom_v1_app.yaml`


8. Start our custom controller
```shell
make run
```

9. Once the controller starting running we have to create the website workload using the yaml that we wrote.
```shell
kubectl create -f config/samples/config/samples/custom_v1_app.yaml
```

10. Verify
```shell
kubectl get app -n <namespace>
kubectl get deployments -n <namespace>
kubectl get pods -n <namespace>
kubectl get svc -n <namespace>
kubectl get hpa -n <namespace>
```

## Run


## Original getting started guide

### Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

#### Running on the cluster
1. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
```

2. Build and push your image to the location specified by `IMG`:

```sh
make docker-build docker-push IMG=<some-registry>/kube-operator-demo:tag
```

3. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/kube-operator-demo:tag
```

#### Uninstall CRDs
To delete the CRDs from the cluster:

```sh
make uninstall
```

#### Undeploy controller
UnDeploy the controller from the cluster:

```sh
make undeploy
```

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
