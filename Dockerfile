# SHA-pinned distroless base — no shell, no package manager, minimal
# attack surface. Refreshed by Dependabot on new distroless releases.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:9be3fcc6abeaf985b5ecce59451acbcbb15e7be39472320c538d0d55a0834edc

# goreleaser stages the compiled binary next to this Dockerfile as
# `corral-sync`; nothing else ships in the image.
COPY corral-sync /usr/local/bin/corral-sync

# nonroot user (65532) provided by the distroless base.
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/corral-sync"]
