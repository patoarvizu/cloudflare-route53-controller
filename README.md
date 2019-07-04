# Cloudflare-Route 53 controller

<!-- TOC -->

- [Cloudflare-Route 53 controller](#cloudflare-route-53-controller)
    - [Intro](#intro)
    - [Description](#description)
    - [Configuration](#configuration)
        - [Cloudflare authentication](#cloudflare-authentication)
        - [AWS authentication](#aws-authentication)
    - [Deploying the controller](#deploying-the-controller)
    - [Important notes about this project](#important-notes-about-this-project)
        - [Only `Ingress`es are supported for now](#only-ingresses-are-supported-for-now)
        - [If both records are the same, the controller will skip them](#if-both-records-are-the-same-the-controller-will-skip-them)
        - [Watch out for Cloudflare limits!](#watch-out-for-cloudflare-limits)
    - [Help wanted!](#help-wanted)

<!-- /TOC -->

## Intro

When running a Kubernetes `Ingress` to expose your services, it's fairly trivial to get out-of-the box support for managing its DNS on Route 53 with components like `kops`'s [dns-controller](https://github.com/kubernetes/kops/tree/master/dns-controller). However, when traffic goes through [Cloudflare](https://www.cloudflare.com/), your DNS automation becomes a bit more complex, since now you have to manage two records: the "public" one in Route 53 (e.g. example.patoarvizu.dev) routing to Cloudflare, and another one in Cloudflare pointing to your "origin" sever, which is presumably managed by the dns-controller through annotations.

Since both Cloudflare and Route 53 have programmatic APIs, it wouldn't be too hard to define in code with Terraform for example (via its [Cloudflare](https://www.terraform.io/docs/providers/cloudflare/index.html) and [AWS](https://www.terraform.io/docs/providers/aws/index.html) providers). But the problem with this approach is that in the abscence of any automation, it's not very helpful when one tries to move quickly. Even with a pipeline, we have the additional problem of coupling two things together that live in different ecosystems, and more likely than not, even different repositories.

It's also possible to use one of the many SDKs for either provider and write a script to automate the task, but there's still a disconnect between the API logic, and the discovery logic. In other words, writing a parameterized script or library to write two DNS records is easy, but discovering what those two values should be, is not that simple. You need a way for such script to run continuously, close to the dynamic source (the `Ingress`es) to handle discovery, is able to handle authentication, can be deployed like your other services and can react to changes quickly, and provides high availability. Clearly, the best option based on the context of the problem is to run it in Kubernetes.

## Description

Using this controller is as simple as adding an annotation! The default annotation prefix is `cloudflare.patoarvizu.dev`, but that's configurable via the `-annotation-prefix` command line argument. If you annotate your `Ingress` with `cloudflare.patoarvizu.dev/cloudflare-record` and assuming you're already creating its DNS with the `dns.alpha.kubernetes.io/external` annotation, this controller will create a `CNAME` Route 53 record in the zone indicated by the `-hosted-zone-id` flag, pointing to Cloudflare in the zone indicated by the `-cloudflare-zone-name` argument, as well as another `CNAME` in Cloudflare pointing to the origin record, defined by the `dns.alpha.kubernetes.io` annotation.

For example, if you have the following:

```
kind: Ingress
metadata:
  annotations:
    cloudflare.patoarvizu.dev/cloudflare-record: example.patoarvizu.dev
    dns.alpha.kubernetes.io/external: example-origin.patoarvizu.dev
```

This controller will create a Route 53 `CNAME example.patoarvizu.dev` pointing to `example.patoarvizu.dev.cdn.cloudflare.net`, and a Cloudflare `CNAME example` in the `patoarvizu.dev` zone, pointing to `example-origin.patoarvizu.dev`. Remember that Cloudflare is not necessarily the authoritative DNS server, so the `CNAME` in the `patoarvizu.dev` Cloudflare zone is not the same as the one in the Route 53 hosted zone.

## Configuration

The controller will require authentication for both Route 53 as well as for Cloudflare.

### Cloudflare authentication

The controller looks for two environment variables, `CLOUDFLARE_EMAIL` and `CLOUDFLARE_TOKEN` and uses those for all operations. As of the current version, it's only possible to authenticate with personal email/token, and support for access service tokens will come in a future version. In any case, it's best practice to use credentials for a service user (i.e. not tied to a human identity) and not personal ones.

### AWS authentication

The controller uses the default [credential precedence order](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html), depending on your setup, you might need to inject access keys, configuration files, or if the controller is running on EC2 nodes, it may get its credentials via the [local metadata endpoint](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html), but in any case, the policy associated with the identity should have IAM permissions for writing to Route 53.

## Deploying the controller

A [Helm](https://helm.sh/) chart is provided in the `helm/cloudflare-route53-controller` directory. This chart depends on two manifests not included in it: a `Secret` (called `cloudflare-route53-controller-secrets` by default) where the `Deployment` can find the `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `CLOUDFLARE_TOKEN` secrets, as well as a `ConfigMap` (called `cloudflare-route53-controller-config` by default), where the `Deployment` can find the `CLOUDFLARE_EMAIL` variable.

## Important notes about this project

### Only `Ingress`es are supported for now

At the moment, the controller will only look for annotations on `Ingress` objects. Support for `Services` will be added in a future release.

### If both records are the same, the controller will skip them

To avoid race conditions or constant overwrites, the controller will skip writing both DNS records if the values of `cloudflare.patoarvizu.dev/cloudflare-record` and `dns.alpha.kubernetes.io/external` are the same.

### Watch out for Cloudflare limits!

Cloudflare may impose limits on the rate of API calls. If you deploy multiple controllers using the same token, or if it controls a high number of `Ingress`es, it may go over the limit. Check the [Cloudflare API documentation](https://api.cloudflare.com/) for more details.

## Help wanted!

Help is always welcome! The author of this controller is not a "real" golang developer and the code probably shows. If you feel like you can contribute to either the code, documentation, testing, features, etc., or even just reporting bugs or typos, please don't hesitate to do so!