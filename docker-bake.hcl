# Every backend image, built in one Bake run by the push path of
# .github/workflows/deploy.yml. One run builds the shared `builder` and
# `build-static` stages of the Dockerfile once for all targets.
#
# Keep `images` in step with the release-path matrix in deploy.yml: the
# release promotes `<image>:<sha>` for exactly these names.
#
#   docker buildx bake --print                       # resolved definition
#   IMAGE_REPO=local SHA=dev docker buildx bake      # build without pushing

variable "IMAGE_REPO" {
  # <region>-docker.pkg.dev/<project>/backend
  default = "local"
}

variable "SHA" {
  default = "dev"
}

variable "images" {
  default = [
    { name = "api", target = "server" },
    { name = "consumer", target = "consumer" },
    { name = "identity", target = "identity" },
    { name = "concert-discovery", target = "concert-discovery" },
    { name = "artist-image-sync", target = "artist-image-sync" },
    { name = "official-site-refresh", target = "official-site-refresh" },
    { name = "sales-phase-discovery", target = "sales-phase-discovery" },
    { name = "sales-reminders", target = "sales-reminders" },
    { name = "series-image-consumer", target = "series-image-consumer" },
  ]
}

group "default" {
  targets = [for img in images : img.name]
}

target "image" {
  name    = img.name
  matrix  = { img = images }
  context = "."
  target  = img.target
  tags = [
    "${IMAGE_REPO}/${img.name}:latest",
    "${IMAGE_REPO}/${img.name}:main",
    "${IMAGE_REPO}/${img.name}:${SHA}",
  ]
  # Layer cache for the stages every image shares (base images, `go mod
  # download`, the libvips packages). One target writes it and all read it:
  # when all nine wrote their own scope, the same shared layers were exported
  # nine times (~60 s per run). series-image-consumer writes it because its
  # stages include `builder` plus the libvips layers. Compiled packages are
  # not in this cache; they live in the Go build cache mount (see Dockerfile
  # and the "Go build cache" steps of deploy.yml).
  cache-from = ["type=gha,scope=backend-shared"]
  cache-to   = img.name == "series-image-consumer" ? ["type=gha,mode=max,scope=backend-shared"] : []
}
