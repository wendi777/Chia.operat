# chia-operator

NOTE:  This  is  a  very early implementation. Features may come in at a fast pace, bugs may exist in plenty, and the CRs themselves may change drastically during this stage. In short: this code is not guaranteed to work. That said, it is being ran as a couple of  farms  on  the  mainnet and testnet10 networks today.

Kubernetes operator for managing Chia components in kubernetes. Currently supported components:

 - full_nodes
 - farmers
 - harvesters
 - wallets

Applying a CR for each component allows you to instantiate a configured instance of that component that is able to communicate to other requisite components in the cluster. A whole farm can be ran with each component isolated in its own pod, with a chia-exporter sidecar to scrape Prometheus metrics.

ChiaCA is an additional CRD that generates a certificate authority for Chia components and places it in a kubernetes Secret as a convenience. Alternatively, users can pre-generate their own CA Secret with data keys for: `chia_ca.crt`, `chia_ca.key`, `private_ca.crt`, and `private_ca.key`.

## Getting Started


### Install the operator

Clone this repository (and change to its directory):
```bash
git clone https://github.com/Chia-Network/chia-operator.git
cd chia-operator
```

Install the CRDs:
```bash
make install
```

Deploy the operator:
```bash
make deploy
```

### Start a farm

This guide installs everything in the default namespace, but you can of course install them in any namespace. These are also all fairly minimal examples with just enough config to be helpful. Other options are supported. See the `config/samples` directory of this repo for more full examples.

#### SSL CA

First thing you'll need is a CA Secret. Chia components all communicate with each other over TLS with signed certificates all using the same certificate authority. This presents a problem in k8s, because each chia-docker container will try to generate their own CAs if none are declared, and all your components will refuse to communicate with each other. This operator contains a ChiaCA CRD that will generate a new CA and set it as a kubernetes Secret for you, or you can make your own Secret with a pre-existing ssl/ca directory. In this guide, we'll show the ChiaCA method first, and then the pre-made Secret second.

Create a file named `ca.yaml`:
```yaml
apiVersion: k8s.chia.net/v1
kind: ChiaCA
metadata:
  name: mainnet-ca
spec:
  secret: mainnet-ca
```

The `spec.secret` key specifies the name of the k8s Secret that will be created. The Secret will be created in the same namespace that the ChiaCA CR was created in. Apply this with `kubectl apply -f ca.yaml`

The ChiaCA exists as an option of convenience, but if you have your own CA you'd like to use instead, you'll need to create a Secret that contains all the files in the `$CHIA_ROOT/config/ssl/ca` directory, like so:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mainnet-ca
data:
  chia_ca.crt: >-
    <redacted base64'd file output>
  chia_ca.key: >-
    <redacted base64'd file output>
  private_ca.crt: >-
    <redacted base64'd file output>
  private_ca.key: >-
    <redacted base64'd file output>
type: Opaque
```
You only need to do this if you don't want to use the ChiaCA CR to make it for you.

#### full_node

Next we need a full_node. Create a file named `node.yaml`:
```yaml
apiVersion: k8s.chia.net/v1
kind: ChiaNode
metadata:
  name: mainnet
spec:
  replicas: 1
  chia:
    caSecretName: mainnet-ca
    timezone: "UTC"
  storage:
    chiaRoot:
      hostPathVolume:
        path: "/home/user/.chia/mainnetk8s"
  nodeSelector:
    kubernetes.io/hostname: "node-with-hostpath"
```

As you can see, we used a hostPath volume for CHIA_ROOT. We also specified a nodeSelector for the full_node pod that will be brought up in this Statefulset, this is because a hostPath won't move over to other nodes in the cluster if it gets scheduled elsewhere. You can configure a persistent volume claim instead. ChiaNode objects create StatefulSets which allow each pod to generate their very own PersistentVolumeClaim with this as your storage config:
```yaml
storage:
  chiaRoot:
    persistentVolumeClaim:
      storageClass: ""
      resourceRequest: "300Gi"
```
Finally, apply your ChiaNode with: `kubectl apply -f node.yaml`

#### farmer

Now we can create a farmer that talks to our full_node. Create a file named `farmer.yaml`:
```yaml
apiVersion: k8s.chia.net/v1
kind: ChiaFarmer
metadata:
  name: mainnet
spec:
  chia:
    caSecretName: mainnet-ca
    timezone: "UTC"
    fullNodePeer: "mainnet-node.default.svc.cluster.local:8444"
    secretKey:
      name: "chiakey"
      key: "key.txt"
```
A couple of things going on here. First, we configured the fullNodePeer address, which we'll use kubernetes internal DNS names for services, targeting port 8444 on the `mainnet-node` service, in the `default` namespace, using the default cluster domain `cluster.local`. If your cluster uses a non-default domain name, switch it to that. Also switch `default` to whatever namespace your ChiaNode is deployed to.

We also have a `secretKey` in the chia config spec. That defines a k8s Secret in the same namespace as this ChiaFarmer, named `chiakey` which contains one data key `key.txt` which contains your Chia mnemonic. 

Finally, apply this ChiaFarmer with `kubectl apply -f farmer.yaml`

#### harvester

Now we can create a harvester that talks to our farmer. Create a file named `harvester.yaml`:

```yaml
apiVersion: k8s.chia.net/v1
kind: ChiaHarvester
metadata:
  name: mainnet
spec:
  chia:
    caSecretName: mainnet-ca
    timezone: "UTC"
    farmerAddress: "mainnet-farmer.default.svc.cluster.local"
  storage:
    plots:
      hostPathVolume:
        - path: "/mnt/plot1"
        - path: "/mnt/plot2"
  nodeSelector:
    kubernetes.io/hostname: "node-with-hostpaths"
```

The config here is very similar to the other components we already made, but we're specifying the farmerAddress, which tells the harvester where to look for the farmer. The farmer port is inferred. And in the storage config, we're specifying two plot directories that are mounted to a particular host. And we're pinning this harvester pod to that node using a nodeSelector with a label that exists on that particular node.

#### wallet

Now we can create a wallet that talks to our full_node. Create a file named `wallet.yaml`:

```yaml
apiVersion: k8s.chia.net/v1
kind: ChiaWallet
metadata:
  name: mainnet
spec:
  chia:
    caSecretName: mainnet-ca
    timezone: "UTC"
    fullNodePeer: "mainnet-node.default.svc.cluster.local:8444"
    secretKey:
      name: "chiakey"
      key: "key.txt"
```

The config here is very similar to the farmer we already made since it also requires your mnemonic key and a full_node peer. 

Finally, apply this ChiaWallet with `kubectl apply -f wallet.yaml`

## TODO

- Add to examples in `config/samples`. The full API for all of this operator's CRDs are shown in Go structs in `api/v1`
- Add support for other Chia components (timelord, dns-introducer, etc)
- Make chia-exporter an optional container in the pod

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
