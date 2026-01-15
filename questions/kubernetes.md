#### 1.控制面组件和作用

控制面组件包括`kube-apiserver`，`etcd`，`kube-controller-manager`，`kube-scheduler`和`cloud-controller- manager`

`kube-apiserver`是外部与Kubernetes集群交互的唯一入口，核心功能是`认证`、`授权`和`准入控制`，是Kubernetes集群的消息总线，负责与`etcd`交互，将obj对象写入`etcd`。

`etcd`是一个分布式kv数据库，负责持久化存储整个集群的配置和状态信息，采用raft一致性算法保证数据一致性和高可用性，支持Watch API与kube-apiserver建立长链接，间接为其他组件提供数据。

`kube-controller-manager`负责维护集群的期望状态，其中包含多个二进制组件，每个控制器组件管理一种资源对象，如果对象的实际状态和期望状态不一致，则通过调谐使其达到期望状态。

`kube-scheduler`负责为Pod选出最优节点，通过在插件化的framework中注册各种plugin，并在对应的阶段执行插件对目标节点做筛选，调度的过程中包括调度周期和异步的绑定周期。

`cloud-controller-manager`使负责与云服务提供商进行交互的组件，它使得Kubernetes集群能够更好地与云环境集成，充分利用云平台提供的各种资源和服务。

#### 2.如果从零开始部署集群

##### 二进制部署

##### kubeadm部署


#### 3.两种集群部署方式有何区别



#### 4.pause容器和其生命周期



#### 5.探针有哪几种

探针是Kubernetes中保障Pod可用性的核心机制，kubelet 通过执行探针检查容器健康状态，并根据结果调整 Pod 生命周期（重启容器、停止转发流量等），核心分为**存活探针**、**就绪探针**、**启动探针**三类。

检测方式：
* exec：在容器内执行指定命令，命令退出码为`0`则成功
* httpGet：向容器指定端口发送HTTP GET请求，返回状态码≥200且<400则成功
* tcpSocket：尝试与容器指定端口建立TCP连接，连接成功则成功

|          探针类型          |    检测目标     |            失败后果             |    生效阶段    |               核心使用场景               |
| :------------------------: |:-----------:| :-----------------------------: | :------------: | :--------------------------------------: |
| 存活探针（livenessProbe）  |   容器是否存活    |            重启容器             | 容器全生命周期 |      应用卡死、进程阻塞，需重启恢复      |
| 就绪探针（readinessProbe） |  容器是否可接收流量  | 移除 Service 端点，停止转发流量 | 容器全生命周期 | 应用启动未完成、临时不可用，避免流量进入 |
|  启动探针（startupProbe）  |  应用是否完成启动   |            重启容器             | 仅容器启动阶段 |       慢启动应用，避免启动阶段误判       |

#### 6.Pod创建过程



#### 7.集群中的事件是如何产生的



#### 8.Pod处于Pending状态如何排查



#### 9.预选和优选的区别

在Pod调度的生命周期里，kube—scheduler的framework中总共设有13个标准扩展点，所谓的预选和优选通常是指其中的`PreFilter/Filter`和`PreScore/Score`扩展点的动作。

在经过扩展点时会遍历执行注册的扩展插件，然后根据其中的算法返回结果，预选的结果是返回一个满足资源要求的节点列表，优选的结果是以这个可用的节点列表为输入，通过一系列算法，返回一个得分最高的Node信息，给后续的资源预留和绑定操作明确的目标。

简而言之一个是找到所有符合条件的，另一个是在符合条件中选出最合适的。



#### 10.容器运行时切换步骤



#### 11.Service如何管理Endpoint



#### 12.边车对服务有什么影响



#### 13.通过Ingress暴露的服务不可用如何排查



#### 14.对静态容器的理解



#### 15.ClusterIP是如何实现的



#### 16.Operator如何开发



#### 17.二进制部署和kubeadm部署集群的对比



#### 18.如何实现集群的高可用



#### 19.Flannel网络插件的实现原理

Flannel是一种轻量级的Kubernetes网络插件，实现简单易于维护，常用于中小型集群中，网络模式包括`VXLAN`和`Host-GW`。

**VXLAN**模式

* 使用`隧道技术`实现`Overlay`网络，在每个节点上会创建一个`VTEP`设备`flannel.1`，负责在节点间建立VXLAN隧道
* 数据包传输时，内层帧包含源/目标Pod的IP地址，源/目标`flannel.1`设备的MAC地址，由flannel添加VXLAN头部包含`VNI`用来标识网络隧道，外层是两个宿主机的IP地址。封装后的数据经过过宿主机的`eht0`网卡转发到目标Pod所在的宿主机，通过`flannel.1`解封装验证并转发给目标Pod
* 该模式仅需要三层IP网络互通

**Host-GW**

* 使用主机充当网关设备进行转发
* 需要节点间二层网络互通
* 不需要额外封装性能高

| **特性**      | **VXLAN模式**                     | **Host-GW模式**                      |
| ------------- | --------------------------------- | ------------------------------------ |
| **网络类型**  | Overlay（隧道化，跨三层）         | Underlay（直接路由，二层直连）       |
| 网络要求      | 三层互通                          | 二层互通                             |
| **封装**      | VXLAN封装                         | 无封装，直接三层转发                 |
| **VTEP 设备** | 依赖 `flannel.1` 进行隧道端点处理 | 无需 VTEP，宿主机直接作为网关        |
| **适用场景**  | 云环境、跨网段、复杂网络架构      | 同机房 IDC、高性能要求、二层直连场景 |
| **性能**      | 中                                | 高                                   |

#### 20.Calico网络插件的实现原理

Calico是常见的网络插件，核心组件包括`Felix`和`BIRD`，`Felix`负责配置本地路由和管理访问控制列表(ACL)，`BIRD`是BGP客户端，会从`Felix`获取路由信息并使用`BGP`协议分发给集群中其他节点的BIRD进程来交换路由信息。支持的网络模式包括`BGP`、`IPIP`和`VXLAN`。

**BGP模式**

* 流量转发过程：pod1的eth0-->veth-pair的另一端cali1-->源主机eth0-->目的主机eth0-->根据目的ip匹配veth-pair的一端cali2-->pod2的eth0
* 需要节点间二层网络互通
* 在大规模集群中，由于运行BGP协议需要学习并维护大量路由表，会影响集群通信的性能，可以通过把全连接改为路由反射来减少每个节点需要维护的对等实体数量。

**IPIP模式**

* 三层封装，直接封装原始IP包(内层)到新的IP包(外层)
* 基于IP协议，无需额外端口，仅需底层网络支持 IP 转发

**VXLAN模式**

* 二层封装，把原始的Ethernet帧(内层)到UDP包(外层)
* 基于UDP协议，需要放开4789端口

| **特性**     | **BGP 模式**             | **IPIP 模式**          | **VXLAN 模式**           |
| ------------ | ------------------------ | ---------------------- | ------------------------ |
| **网络类型** | Underlay（物理网络直连） | Overlay（IPIP 隧道）   | Overlay（VXLAN 隧道）    |
| **封装协议** | 无封装                   | IP-in-IP（协议号 4）   | UDP+VXLAN（端口 4789）   |
| **核心设备** | 无隧道设备（依赖路由表） | `tunl0` 隧道设备       | `vxlan.cali` VTEP 设备   |
| **网络隔离** | 不支持（基于物理网络）   | 不支持（依赖 IP 子网） | 支持（VNI 标识不同网络） |
| **性能开销** | 无（最优）               | 低（20 字节封装）      | 中（50 字节封装）        |

#### 21.Calico和Flannel的对比

| 维度       | Flannel                       | Calico               |
| ---------- | ----------------------------- | -------------------- |
| 网络模型   | Overlay                       | Underlay/Overlay     |
| 策略能力   | 基础 L3/L4                    | 高级 L3-L7           |
| 性能       | VXLAN: 中；Host-GW: 高        | BGP: 高；IPIP: 中    |
| 扩展性     | <500 节点                     | >1000 节点           |
| 安全特性   | 依赖 Kubernetes NetworkPolicy | 内置 RBAC + 审计日志 |
| 多租户支持 | 无                            | 通过 NetworkSet 实现 |

#### 22.CRI都提供哪些服务，如何工作


#### 23.CNI都提供哪些服务，如何工作


#### 24.CSI都提供哪些服务，如何工作


#### 25.kube-proxy的作用，iptables和ipvs模式的区别

**kube-proxy的作用**：

kube-proxy是Kubernetes集群中每个节点上运行的网络代理组件，是Service功能的核心实现，主要职责包括：

1. Service流量代理：将访问Service的ClusterIP/NodePort/LoadBalancer的流量，转发到后端对应的Pod（通过Endpoint匹配），提供四层负载均衡。 
2. 转发规则动态维护：实时监听API Server中Service和Endpoint的创建/更新/删除事件，动态同步更新节点上的网络转发规则（iptables规则或ipvs规则），保证规则与集群状态一致。
3. 会话亲和性支持：支持SessionAffinity（会话亲和性），默认是None（无亲和性）；当配置为ClientIP模式时，可确保同一客户端IP的请求持续转发到同一个后端Pod。

**iptables模式**：

**工作原理**：

依赖Linux内核Netfilter框架的iptables功能，通过“表-链-规则”的层级结构实现流量转发

1. **表结构**：iptables包含四个表：
* `filter`：包过滤
* `nat`：地址转换
* `mangle`：包修改
* `raw`：连接跟踪

2. **链结构**：每个表包含预定义链和自定义链：
* 预定义链：`PREROUTING`、`POSTROUTING`、`INPUT`、`OUTPUT`、`FORWARD`
* 自定义链：kube-proxy为每个Service创建自定义链，如`KUBE-SVC-xxx`、`KUBE-SEP-xxx`

3. **规则生成**：
* 当创建Service时，kube-proxy在`nat`表的`OUTPUT`和`PREROUTING`链中添加规则，Pod访问ClusterIP新增`OUTPUT`链规则，外部访问CLusterIP新增`PREROUTING`链规则
* 为每个Service创建`KUBE-SVC-xxx`链，实现负载均衡
* 为每个Endpoint创建`KUBE-SEP-xxx`链，指向具体的Pod IP和端口
* 当流量匹配Service地址时，先进入KUBE-SVC-xxx链，通过算法选择一个KUBE-SEP-xxx链，最终转发到目标Pod。

**缺点**：
- **性能问题**：规则数量多时，iptables采用链式匹配，需要遍历规则链，时间复杂度O(n)
- **更新开销**：每次Service或Endpoint变化，旧版需要重新加载整个iptables规则集会导致网络性能抖动，现在已经是增量修改但仍可能存在iptables规则锁竞争带来的性能影响
- **规则膨胀**：大规模集群中规则数量可能达到数万条，影响性能
- **不支持高级负载均衡算法**：只支持random和rr算法

**IPVS（IP Virtual Server）模式**：

**工作原理**：

1. **核心概念**：
* **Virtual Server（VS）**：对应Kubernetes的Service，使用ClusterIP和端口
* **Real Server（RS）**：对应Kubernetes的Pod，使用Pod IP和端口

2. **工作流程**：
* kube-proxy监听API Server，当Service或Endpoint变化时，调用内核netlink接口更新ipvs规则
* 创建Virtual Server（对应Service的ClusterIP）
* 添加Real Server（对应后端Pod）
* 配置调度算法和会话保持

3. **支持的调度算法**：
* `rr`（Round Robin）：轮询
* `lc`（Least Connection）：最少连接
* `dh`（Destination Hashing）：目标地址哈希
* `sh`（Source Hashing）：源地址哈希
* `sed`（Shortest Expected Delay）：最短预期延迟
* `nq`（Never Queue）：永不排队

4. **数据转发**：

ipvs使用哈希表查找，时间复杂度O(1)

支持三种转发模式：
* **NAT模式**：修改数据包的源/目标IP和端口
* **Tunneling模式**：使用IPIP隧道封装
* **Direct Routing模式**：直接路由，性能最高但需要配置

**优点**：
- **高性能**：使用哈希表查找，时间复杂度O(1)，性能远高于iptables
- **支持多种调度算法**：提供丰富的负载均衡算法选择
- **规则更新效率高**：只更新变化的规则，无需重载整个规则集
- **适合大规模集群**：可以支持数万个Service和Endpoint
- **更好的连接跟踪**：ipvs的连接跟踪机制更高效

**缺点**：
- 需要加载额外内核模块
- 某些云环境可能不支持ipvs模块
- 配置相对复杂

**对比**：

| 维度         | iptables模式               | ipvs模式             |
|------------|--------------------------|--------------------|
| **查找性能**   | O(n)链式匹配                 | O(1)哈希表查找          |
| **规则更新**   | 需要重载整个规则集                | 只更新变化的规则           |
| **负载均衡算法** | 仅支持random、rr             | 支持rr、lc、dh、sh等多种算法 |
| **转发模式**   | 仅NAT                     | 支持NAT、Tunneling、DR |
| **规则数量限制** | 大规模Service/EndPoint场景性能下降明显 | 可支持数万条规则           |
| **内核依赖**   | 无需额外模块（系统自带）             | 需要加载ipvs内核模块       |
| **适用场景**   | 中小规模集群（<1000节点）          | 大规模集群（>1000节点）     |
| **性能**     | 中                        | 高                  |

#### 26.节点NotReady如何排查


#### 27.节点出现严重的负载不均衡如何处理


#### 28.Pod的IP地址用完了怎么办


#### 29.批量调度是如何实现的


#### 30.详细说明Informer机制和相关组件


#### 31.Kubernetes中存在哪几种负载均衡