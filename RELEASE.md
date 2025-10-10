Release procedure
=================

This document describes how to release a new version of MOCO.

## Versioning

Follow [semantic versioning 2.0.0][semver] to choose a new version number.

## Prepare change log entries

Add notable changes since the last release to [CHANGELOG.md](CHANGELOG.md).
It should look like:

```markdown
(snip)
## [Unreleased]

### Added
- Implement ... (#35)

### Changed
- Fix a bug in ... (#33)

### Removed
- Deprecated `-option` is removed ... (#39)

(snip)
```

## Bump version

1. Determine a new version number. Then set `VERSION` variable.

    ```console
    # Set VERSION and confirm it. It should not have "v" prefix.
    # Patch version starts with 0
    $ VERSION=x.y.z
    $ echo $VERSION
    ```

2. Make a new branch from the latest `main` with `git checkout -b bump-v$VERSION`.

    ```console
    $ git checkout main
    $ git pull
    $ git checkout -b "bump-v$VERSION"
    ```

3. Update version strings in `kustomization.yaml` and `version.go`.
4. Edit `CHANGELOG.md` for the new version ([example][]).
5. Commit the change and create a pull request:

    ```console
    $ git commit -a -m "Bump version to v$VERSION"
    $ git push -u origin HEAD
    $ gh pr create -f
    ```

6. Merge the new pull request.
7. Add a new tag and push it as follows:

    ```console
    # Set VERSION again.
    $ VERSION=x.y.z
    $ echo $VERSION

    $ git checkout main
    $ git pull
    $ git tag -a -m "Release v$VERSION" v$VERSION

    # Make sure the release tag exists.
    $ git tag -ln | grep $VERSION

    $ git push origin v$VERSION
    ```

8. (Option) Edit GitHub release page
    You may edit [the GitHub release page](https://github.com/cybozu-go/moco/releases/latest) to add further details.

## Bump Chart Version

MOCO Helm Chart will be released independently.
This will prevent the MOCO version from going up just by modifying the Helm Chart.

1. Determine a new version number:

    ```console
    # Set variables. They should not have "v" prefix.
    $ APPVERSION=x.y.z # MOCO version
    $ AGENTVERSION=j.k.l # MOCO Agent version
    $ EXPORTERVERSION=d.e.f.g # mysqld_exporter version, see version.go
    $ FLUENTBITVERSION=h.i.j.k # FluentBit version, see version.go
    $ CHARTVERSION=a.b.c
    $ echo $APPVERSION $CHARTVERSION
    $ echo $AGENTVERSION $EXPORTERVERSION $FLUENTBITVERSION
    ```

2. Make a new branch from the latest `main` with `git checkout -b bump-chart-v$CHARTVERSION`.

    ```console
    $ git checkout main
    $ git pull
    $ git checkout -b "bump-chart-v$CHARTVERSION"
    ```

3. Update version strings:

    ```console
    $ sed -r -i "s/^(appVersion: )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/\1${APPVERSION}/g" charts/moco/Chart.yaml
    $ sed -r -i "s/^(version: )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/\1${CHARTVERSION}/g" charts/moco/Chart.yaml
    $ sed -r -i "s/(tag: +# )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/\1${APPVERSION}/g" charts/moco/values.yaml
    $ sed -r -i "s/(tag: )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+( +# agent.image.tag)/\1${AGENTVERSION}\2/g" charts/moco/values.yaml
    $ sed -r -i "s/(tag: )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]+\.[[:digit:]]+( +# fluentbit.image.tag)/\1${FLUENTBITVERSION}\2/g" charts/moco/values.yaml
    $ sed -r -i "s/(tag: )[[:digit:]]+\.[[:digit:]]+\.[[:digit:]+\.[[:digit:]]+( +# mysqldExporter.image.tag)/\1${EXPORTERVERSION}\2/g" charts/moco/values.yaml
    ```

4. Edit `charts/moco/CHANGELOG.md` for the new version ([example][]).
5. Commit the change and create a pull request:

    ```console
    $ git commit -a -m "Bump chart version to v$CHARTVERSION"
    $ git push -u origin HEAD
    $ gh pr create -f
    ```

6. Merge the new pull request.
7. Add a new tag and push it as follows:

    ```console
    # Set CHARTVERSION again.
    $ CHARTVERSION=a.b.c
    $ echo $CHARTVERSION

    $ git checkout main
    $ git pull
    $ git tag -a -m "Release chart-v$CHARTVERSION" chart-v$CHARTVERSION

    # Make sure the release tag exists.
    $ git tag -ln | grep $CHARTVERSION

    $ git push origin chart-v$CHARTVERSION
    ```

## Container Image Release

This repository manages the following container images that MOCO uses:

* [MySQL](./containers/mysql)
* [fluent-bit](./containers/fluent-bit)
* [mysqld_exporter](./containers/mysqld_exporter)

If you want to release these images, edit the TAG file.

e.g. [containers/mysql/8.0.32/TAG](./containers/mysql/8.0.32/TAG)

When a commit changing the TAG file is merged into the main branch, the release of the container image is executed.
If the TAG file is not changed, the release will not be executed even if you edit the Dockerfile.

### Tag naming

Images whose upstream version conform to [Semantic Versioning 2.0.0][semver] should be
tagged like this:

    Upstream version + "." + Container image version

For example, if the upstream version is `X.Y.Z`, the first image for this version will
be tagged as `X.Y.Z.1`.  Likewise, if the upstream version has pre-release part like
`X.Y.Z-beta.3`, the tag will be `X.Y.Z-beta.3.1`.
The container image version will be incremented when some changes are introduced to the image.

If the upstream version has no patch version (`X.Y`), fill the patch version with 0 then
add the container image version _A_ (`X.Y.0.A`).

The container image version _must_ be reset to 1 when the upstream version is changed.

#### Example

If the upstream version is "1.2.0-beta.3", the image tag must begin with "1.2.0-beta.3.1".

[semver]: https://semver.org/spec/v2.0.0.html
[example]: https://github.com/cybozu-go/etcdpasswd/commit/77d95384ac6c97e7f48281eaf23cb94f68867f79
