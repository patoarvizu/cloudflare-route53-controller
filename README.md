# Cloudflare-Route 53 controller

![CircleCI](https://img.shields.io/circleci/build/github/patoarvizu/cloudflare-route53-controller.svg?label=CircleCI) ![GitHub tag (latest SemVer)](https://img.shields.io/github/tag/patoarvizu/cloudflare-route53-controller.svg) ![Docker Pulls](https://img.shields.io/docker/pulls/patoarvizu/cloudflare-route53-controller.svg) ![Keybase BTC](https://img.shields.io/keybase/btc/patoarvizu.svg) ![Keybase PGP](https://img.shields.io/keybase/pgp/patoarvizu.svg) ![GitHub](https://img.shields.io/github/license/patoarvizu/cloudflare-route53-controller.svg)

<!-- TOC -->

- [Cloudflare-Route 53 controller](#cloudflare-route-53-controller)
    - [Intro](#intro)
    - [Motivation (with a warning)](#motivation-with-a-warning)
        - [The warning](#the-warning)
    - [Description](#description)
    - [Configuration](#configuration)
        - [Cloudflare authentication](#cloudflare-authentication)
        - [AWS authentication](#aws-authentication)
    - [Deploying the controller](#deploying-the-controller)
    - [Logging](#logging)
    - [Command line parameters](#command-line-parameters)
    - [For security nerds](#for-security-nerds)
        - [Docker images are signed and published to Docker Hub's Notary server](#docker-images-are-signed-and-published-to-docker-hubs-notary-server)
        - [Docker images are labeled with Git and GPG metadata](#docker-images-are-labeled-with-git-and-gpg-metadata)
    - [Important notes about this project](#important-notes-about-this-project)
        - [The controller doesn't handle deletions](#the-controller-doesnt-handle-deletions)
        - [Only `Ingress`es are supported for now](#only-ingresses-are-supported-for-now)
        - [If both records are the same, the controller will skip them](#if-both-records-are-the-same-the-controller-will-skip-them)
        - [Watch out for Cloudflare limits!](#watch-out-for-cloudflare-limits)
    - [Help wanted!](#help-wanted)

<!-- /TOC -->

## Intro

When running a Kubernetes `Ingress` to expose your services, it's fairly trivial to get out-of-the box support for managing its DNS on Route 53 with components like `kops`'s [dns-controller](https://github.com/kubernetes/kops/tree/master/dns-controller). However, when traffic goes through [Cloudflare](https://www.cloudflare.com/), your DNS automation becomes a bit more complex, since now you have to manage two records: the "public" one in Route 53 (e.g. example.patoarvizu.dev) routing to Cloudflare, and another one in Cloudflare pointing to your "origin" sever, which is presumably managed by the dns-controller through annotations.

Since both Cloudflare and Route 53 have programmatic APIs, it wouldn't be too hard to define in code with Terraform for example (via its [Cloudflare](https://www.terraform.io/docs/providers/cloudflare/index.html) and [AWS](https://www.terraform.io/docs/providers/aws/index.html) providers). But the problem with this approach is that in the absence of any automation, it's not very helpful when one tries to move quickly. Even with a pipeline, we have the additional problem of coupling two things together that live in different ecosystems, and more likely than not, even different repositories.

It's also possible to use one of the many SDKs for either provider and write a script to automate the task, but there's still a disconnect between the API logic, and the discovery logic. In other words, writing a parameterized script or library to write two DNS records is easy, but discovering what those two values should be, is not that simple. You need a way for such script to run continuously, close to the dynamic source (the `Ingress`es) to handle discovery, is able to handle authentication, can be deployed like your other services and can react to changes quickly, and provides high availability. Clearly, the best option based on the context of the problem is to run it in Kubernetes.

## Motivation (with a warning)

The motivation for writing this controller comes after the events of two Cloudflare outages within 10 days (on [06/24/2019](https://blog.cloudflare.com/how-verizon-and-a-bgp-optimizer-knocked-large-parts-of-the-internet-offline-today/) and [07/02/2019](https://blog.cloudflare.com/cloudflare-outage/)). Not being able to restore service to downstream consumers when upstream vendors fail can be costly, and part of the goals of this controller is to help minimize disruptions in an automated way.

### The warning

If you have Cloudflare as part of your setup, it's very likely because of the security features it provides. Because of that, bypassing Cloudflare to direct traffic directly to your origin in case of an outage should not be your first mitigation action. This can make your systems immediately vulnerable and make the cost of continuous operation more costly than the service interruption, which depending on your environment, can have financial or legal repercussions. This controller can be used to make such move very easily, but it's not its explicit goal. Always make changes carefully and make sure you weigh the pros and cons of any emergency action.

## Description

Using this controller is as simple as adding an annotation! The default annotation prefix is `cloudflare.patoarvizu.dev`, but that's configurable via the `-annotation-prefix` command line argument. If you annotate your `Ingress` with `cloudflare.patoarvizu.dev/cloudflare-record` and assuming you're already creating its DNS with the `dns.alpha.kubernetes.io/external` annotation, this controller will create a `CNAME` Route 53 record in the zone indicated by the `-hosted-zone-id` flag, pointing to Cloudflare in the zone indicated by the `-cloudflare-zone-name` argument, as well as another `CNAME` in Cloudflare pointing to the origin record, defined by the `dns.alpha.kubernetes.io` annotation.

For example, if you have the following:

```
kind: Ingress
metadata:
  annotations:
    cloudflare.patoarvizu.dev/cloudflare-record: example.patoarvizu.dev
    dns.alpha.kubernetes.io/external: example-origin.patoarvizu.dev
...
```

This controller will create a Route 53 `CNAME example.patoarvizu.dev` pointing to `example.patoarvizu.dev.cdn.cloudflare.net`, and a Cloudflare `CNAME example` in the `patoarvizu.dev` zone, pointing to `example-origin.patoarvizu.dev`. Remember that Cloudflare is not necessarily the authoritative DNS server, so the `CNAME` in the `patoarvizu.dev` Cloudflare zone is not the same as the one in the Route 53 hosted zone.

You can also have the controller create an additional set of DNS records for each `Host` in the Ingress rules. For safety, to prevent accidental creation of records that may already be managed by an external process, this feature needs two configuration points. First, the controller needs to be launched with the `-enable-additional-hosts-annotations` flag. This enables the use of the `cloudflare.patoarvizu.dev/add-rules-hosts` annotation. When this annotation is set to a truthy value, the controller will create the additional DNS records.

For example, if you have this:

```
kind: Ingress
metadata:
  annotations:
    cloudflare.patoarvizu.dev/cloudflare-record: example.patoarvizu.dev
    dns.alpha.kubernetes.io/external: example-origin.patoarvizu.dev
spec:
  rules:
  - host: host1.patoarvizu.dev
...
```

The controller will create the record indicated by the `cloudflare.patoarvizu.dev/cloudflare-record`, and additionally, it will create another `CNAME host1.patoarvizu.dev` pointing to `host1.patoarvizu.dev.cdn.cloudflare.net`, and the corresponding one pointing from Cloudflare to `example-origin.patoarvizu.dev`.

Similarly, you can have the controller create additional records based on an existing `ingress.kubernetes.io/server-alias` annotation. If you add the `cloudflare.patoarvizu.dev/add-aliases` annotation, the controller will create a new set of records for each alias.

Just keep in mind that the `-enable-additional-hosts-annotations` flag enables the annotation across all `Ingress`es but **doesn't** enforce it, while the `cloudflare.patoarvizu.dev/add-rules-hosts` or `cloudflare.patoarvizu.dev/add-aliases` annotations can be applied selectively to a subset of them.

## Configuration

The controller will require authentication for both Route 53 as well as for Cloudflare.

### Cloudflare authentication

The controller looks for two environment variables, `CLOUDFLARE_EMAIL` and `CLOUDFLARE_TOKEN` and uses those for all operations. As of the current version, it's only possible to authenticate with personal email/token, and support for access service tokens will come in a future version. In any case, it's best practice to use credentials for a service user (i.e. not tied to a human identity) and not personal ones.

### AWS authentication

The controller uses the default [credential precedence order](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html), depending on your setup, you might need to inject access keys, configuration files, or if the controller is running on EC2 nodes, it may get its credentials via the [local metadata endpoint](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html), but in any case, the policy associated with the identity should have IAM permissions for writing to Route 53.

## Deploying the controller

A [Helm](https://helm.sh/) chart is provided in the `helm/cloudflare-route53-controller` directory. This chart depends on two manifests not included in it: a `Secret` (called `cloudflare-route53-controller-secrets` by default) where the `Deployment` can find the `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `CLOUDFLARE_TOKEN` secrets, as well as a `ConfigMap` (called `cloudflare-route53-controller-config` by default), where the `Deployment` can find the `CLOUDFLARE_EMAIL` variable.

Optionally, if the controller is already running on an environment where it can auto-discover its AWS access keys, e.g. on a node with an instance role that would provide the credentials via the local metadata endpoint, or injected dynamically via [vaultenv](https://github.com/channable/vaultenv), you can set the `aws.withCredentials` value to `false`, and Helm won't render the corresponding environment variables.

## Logging

The controller will log all its output to `stdout`. Additionally, it'll create `Events` that you can see using `kubectl describe ingress`. The controller will emit an event of type `Normal` and reason `Synced` when it successfully processed and created all DNS records, or if there's at least one failure, it'll publish an event of type `Warninr` with reason `Error`.

## Command line parameters


Parameter | Description | Default value
----------|-------------|--------------
`-kubeconfig` | Path to a kubeconfig file. |
`-master` | The address of the Kubernetes API server. Overrides any value in kubeconfig. |
`-annotation-prefix` | The prefix to be used for discovery of managed ingresses. | `cloudflare.patoarvizu.dev`
`-hosted-zone-id` | The id of the Route53 hosted zone to be managed. |
`-cloudflare-zone-name` | The name of the Cloudflare zone to be managed. |
`-enable-additional-hosts-annotations` | Enable flag that allows creating additional records for heach 'Host' in the ingress rules. | `false`
`-frequency` | The frequency at which the controller runs, in seconds. | `30`

## For security nerds

### Docker images are signed and published to Docker Hub's Notary server

The [Notary](https://github.com/theupdateframework/notary) project is a CNCF incubating project that aims to provide trust and security to software distribution. Docker Hub runs a Notary server at https://notary.docker.io for the repositories it hosts.

[Docker Content Trust](https://docs.docker.com/engine/security/trust/content_trust/) is the mechanism used to verify digital signatures and enforce security by adding a validating layer.

You can inspect the signed tags for this project by doing `docker trust inspect --pretty docker.io/patoarvizu/cloudflare-route53-controller`, or (if you already have `notary` installed) `notary -d ~/.docker/trust/ -s https://notary.docker.io list docker.io/patoarvizu/cloudflare-route53-controller`.

If you run `docker pull` with `DOCKER_CONTENT_TRUST=1`, the Docker client will only pull images that come from registries that have a Notary server attached (like Docker Hub).

### Docker images are labeled with Git and GPG metadata

In addition to the digital validation done by Docker on the image itself, you can do your own human validation by making sure the image's content matches the Git commit information (including tags if there are any) and that the GPG signature on the commit matches the key on the commit on github.com.

For example, if you run `docker pull patoarvizu/cloudflare-route53-controller:ecfcf2352f12101d9b2608e53f459149914d8c16` to pull the image tagged with that commit id, then run `docker inspect patoarvizu/cloudflare-route53-controller:ecfcf2352f12101d9b2608e53f459149914d8c16 | jq -r '.[0].ContainerConfig.Labels'` (assuming you have [jq](https://stedolan.github.io/jq/) installed) you should see that the `GIT_COMMIT` label matches the tag on the image. Furthermore, if you go to https://github.com/patoarvizu/cloudflare-route53-controller/commit/ecfcf2352f12101d9b2608e53f459149914d8c16 (notice the matching commit id), and click on the **Verified** button, you should be able to confirm that the GPG key ID used to match this commit matches the value of the `SIGNATURE_KEY` label, and that the key belongs to the `AUTHOR_EMAIL` label. When an image belongs to a commit that was tagged, it'll also include a `GIT_TAG` label, to further validate that the image matches the content.

Keep in mind that this isn't tamper-proof. A malicious actor with access to publish images can create one with malicious content but with values for the labels matching those of a valid commit id. However, when combined with Docker Content Trust, the certainty of using a legitimate image is increased because the chances of a bad actor having both the credentials for publishing images, as well as Notary signing credentials are significantly lower and even in that scenario, compromised signing keys can be revoked or rotated.

Here's the list of included Docker labels:

- `AUTHOR_EMAIL`
- `COMMIT_TIMESTAMP`
- `GIT_COMMIT`
- `GIT_TAG`
- `SIGNATURE_KEY`

## Important notes about this project

### The controller doesn't handle deletions

Since this controller is stateless, it's not aware of what operations have been done in the past. Because of that it can't tell if the absence of an annotation (or a whole `Ingress`) means it was removed and it needs to delete de associated records, or if it never existed in the first place. To keep the controller logic stateless and as simple as possible, deletions are outside of its scope.

**This means that the controller may permanently overwrite records managed or created externally, so be careful.** If you have records that are managed manually, via Terraform, [Pulumi](https://www.pulumi.com/), scripts, etc. **that are not running continuously** you may have to re-create them. If you have some other automated (and continuous) way of creating your DNS records, the risk of losing them is less, but be aware that they'll keep overwriting each other. This includes records managed by this same controller! I.e. if you have the same `cloudflare.patoarvizu.dev/cloudflare-record` annotation in more than one controller, you'll have a continuous race condition.

### Only `Ingress`es are supported for now

At the moment, the controller will only look for annotations on `Ingress` objects. Support for `Services` will be added in a future release.

### If both records are the same, the controller will skip them

To avoid race conditions or constant overwrites, the controller will skip writing both DNS records if the values of `cloudflare.patoarvizu.dev/cloudflare-record` and `dns.alpha.kubernetes.io/external` are the same.

### Watch out for Cloudflare limits!

Cloudflare may impose limits on the rate of API calls. If you deploy multiple controllers using the same token, or if it controls a high number of `Ingress`es, it may go over the limit. Check the [Cloudflare API documentation](https://api.cloudflare.com/) for more details.

## Help wanted!

Help is always welcome! The author of this controller is not a "real" golang developer and the code probably shows. If you feel like you can contribute to either the code, documentation, testing, features, etc., or even just reporting bugs or typos, please don't hesitate to do so!