Release procedure
=================

This document describes how to release a new version of MOCO.

Versioning
----------

Follow [semantic versioning 2.0.0][semver] to choose the new version number.

Prepare change log entries
--------------------------

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

Bump version
------------

1. Determine a new version number.  Export it as an environment variable:

    ```console
    $ VERSION=1.2.3
    ```

2. Checkout `main` branch.
3. Make a branch to release, for example by `git neco dev bump-v$VERSION`
4. Update `version.go`.
5. Edit `CHANGELOG.md` for the new version ([example][]).
6. Commit the change and create a pull request:

    ```console
    $ git commit -a -m "Bump version to $VERSION"
    $ git neco review
    ```

7. Merge the new pull request.
8. Add a new tag and push it as follows:

    ```console
    $ git checkout main
    $ git pull
    $ git tag v$VERSION
    $ git push origin v$VERSION
    ```

## (Option) Edit GitHub release page

If you would like to give further descriptions of the new release,
edit [the GitHub release page](https://github.com/cybozu-go/moco/releases/latest).

[semver]: https://semver.org/spec/v2.0.0.html
[example]: https://github.com/cybozu-go/etcdpasswd/commit/77d95384ac6c97e7f48281eaf23cb94f68867f79
