# How it's made

## Links
* [< Back | Main readme](../README.md)

### Installation prerequisites:

- Kubernetes cluster (local or remote)
- Kubebuilder v3 installed (version 3.0.0 or higher)
- Docker installed and configured
- Go programming language installed (version 1.16 or higher)

### Instructions
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
