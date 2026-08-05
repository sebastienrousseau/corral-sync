# SHA-pinned distroless base — no shell, no package manager, minimal
# attack surface. Refreshed by Dependabot on new distroless releases.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

# goreleaser stages the compiled binary next to this Dockerfile as
# `corral-sync`; nothing else ships in the image.
COPY corral-sync /usr/local/bin/corral-sync

# nonroot user (65532) provided by the distroless base.
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/corral-sync"]
