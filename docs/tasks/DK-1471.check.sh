#!/bin/sh
set -eu
: "${DEVKIT_STAND_LLAMA_SWAP:?укажите абсолютный путь к проверенному llama-swap v166}"
: "${DEVKIT_STAND_LITELLM:?укажите абсолютный путь к изолированному LiteLLM 1.63.14}"
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
devkitctl test "$root/internal" -run 'TestPrivateSourcesStayLocalWhenUnlocked|TestMalformedSourceIsNotDefaultThought|TestRouteRoundTrip' -v
devkitctl test "$root/tools/taskctl" -run 'TestTeamAddRouteRoundTrip|TestPromotedPrivateSourcePreservesProtectionWithoutLock' -v
devkitctl test "$root/tools/agentctl" -run 'TestRoute|TestPickLocalParticipant|TestExecBlocksPrivateSourcesWithoutLock|TestRealRouterStand' -v
