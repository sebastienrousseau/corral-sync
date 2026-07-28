# SHA-pinned distroless base — no shell, no package manager, minimal
# attack surface. Refreshed by Dependabot on new distroless releases.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f

# goreleaser stages the compiled binary next to this Dockerfile as
# `corral-sync`; nothing else ships in the image.
COPY corral-sync /usr/local/bin/corral-sync

# nonroot user (65532) provided by the distroless base.
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/corral-sync"]
