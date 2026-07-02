# SHA-pinned distroless base — no shell, no package manager, minimal
# attack surface. Refreshed by Dependabot on new distroless releases.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639

# goreleaser stages the compiled binary next to this Dockerfile as
# `corral-sync`; nothing else ships in the image.
COPY corral-sync /usr/local/bin/corral-sync

# nonroot user (65532) provided by the distroless base.
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/corral-sync"]
