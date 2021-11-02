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

1. Determine a new version number.  Export it as an environment variable:

    ```console
    $ VERSION=1.2.3
    ```

2. Make a new branch from the latest `main` with `git neco dev bump-v$VERSION`.
3. Update version strings in `kustomization.yaml` and `version.go`.
4. Edit `CHANGELOG.md` for the new version ([example][]).
5. Commit the change and create a pull request:

    ```console
    $ git commit -a -m "Bump version to $VERSION"
    $ git neco review
    ```

6. Merge the new pull request.
7. Add a new tag and push it as follows:

    ```console
    $ git checkout main
    $ git pull
    $ git tag -a -m "Release v$VERSION" v$VERSION
    $ git push origin v$VERSION
    ```

## Bump Chart Version

MOCO Helm Chart will be released independently.
This will prevent the MOCO version from going up just by modifying the Helm Chart.

1. Determine a new version number:

    ```console
    $ APPVERSION=1.2.3 # MOCO version
    $ CHARTVERSION=4.5.6
    ```

2. Make a new branch from the latest `main` with `git neco dev bump-chart-v$CHARTVERSION`.
3. Update version strings:

    ```console
    $ sed -r -i "s/^appVersion: [[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/appVersion: ${APPVERSION}/g" charts/moco/Chart.yaml
    $ sed -r -i "s/^version: [[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/version: ${CHARTVERSION}/g" charts/moco/Chart.yaml
    $ sed -r -i "s/tag: # [[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+/tag: # ${APPVERSION}/g" charts/moco/values.yaml
    ```

4. Edit `charts/moco/CHANGELOG.md` for the new version ([example][]).
5. Commit the change and create a pull request:

    ```console
    $ git commit -a -m "Bump chart version to $CHARTVERSION"
    $ git neco review
    ```

6. Merge the new pull request.
7. Add a new tag and push it as follows:

    ```console
    $ git checkout main
    $ git pull
    $ git tag -a -m "Release chart-v$CHARTVERSION" chart-v$CHARTVERSION
    $ git push origin chart-v$CHARTVERSION
    ```

## (Option) Edit GitHub release page

You may edit [the GitHub release page](https://github.com/cybozu-go/moco/releases/latest) to add further details.

[semver]: https://semver.org/spec/v2.0.0.html
[example]: https://github.com/cybozu-go/etcdpasswd/commit/77d95384ac6c97e7f48281eaf23cb94f68867f79
