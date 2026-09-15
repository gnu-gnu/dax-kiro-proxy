# dax-kiro-proxy: Developer Agent Exchange (DAX) proxy for Kiro

Claude Code를 평소처럼 쓰면서, 모델 추론은 로컬에 로그인된 Kiro CLI로 처리하게 해 주는 로컬 프록시입니다.

> **Kiro 관련 고지.** 이 프록시는 Kiro의 동작에 임의로 간섭하지 않습니다.
> Kiro 실행 파일과 사용자의 Kiro 설정은 수정하지 않습니다. 저장된 Kiro 인증 정보는 읽거나 복사하거나 바꾸지 않으며, 로그인은 Kiro가 직접 처리합니다.
> 프록시는 요청을 중개하는 역할만 하며, ACP를 비롯하여 Kiro가 노출하는 기능만을 활용합니다.
> 도구 실행 제한은 실행마다 새로 만들고 종료 시 지우는 임시 Kiro 설정과 에이전트 정의로만 적용합니다.

> **개발 빌드입니다.** macOS Apple silicon(arm64)만 지원하며, 정식 릴리스 기준은 아직 통과하지 않았습니다.

## 역할

- Claude Code가 실행되는 동안만 유지되는 인증된 루프백 게이트웨이를 엽니다. 이 게이트웨이는 Anthropic Messages 형식으로 요청을 받습니다.
- 각 대화를 Kiro의 ACP(Agent Client Protocol) 세션으로 변환합니다.
- Kiro가 요청한 도구 호출을 Claude Code로 돌려보냅니다. Kiro에는 직접 실행 권한이 없습니다.
- `run` 명령 하나로 Kiro 로그인 확인, 임시 클라이언트 프로필 준비, 게이트웨이 시작, Claude Code 실행, 종료 후 정리까지 처리합니다.

## 철학

- **실행 권한은 클라이언트에만 있습니다.** 파일 수정이나 셸 명령 같은 도구는 Claude Code가 자신의 권한과 훅 규칙으로 실행합니다. 프록시와 Kiro는 도구를 직접 실행하지 않습니다.
- **원본 Claude Code 설정을 바꾸지 않습니다.** 실행마다 임시 프로필을 만들고 종료 시 지웁니다. 유일한 예외는 프로젝트 신뢰 질문에 "예"로 답한 값 하나를 `~/.claude.json`에 기록하는 것입니다(D118).
- **Kiro 밖으로 조용히 넘어가지 않습니다.** 다른 모델 제공자로 대체 연결하지 않습니다. 하위 프로세스 환경에서는 Anthropic 등 다른 제공자의 자격 증명과 설정 변수를 제거합니다.
- **로컬에서만, 인증하고, 상한을 둡니다.** 루프백 주소에만 바인딩하고 모든 모델 경로를 인증합니다. 프로세스, 요청, 큐, 대기 중인 도구 호출에는 모두 상한이 있습니다.
- **측정한 것만 주장합니다.** 실제로 측정한 버전 조합을 기준으로 삼고, 측정하지 않은 빌드는 따로 표시합니다. 추정 토큰을 청구 토큰처럼 보고하지 않고, 검증하지 못한 동작은 제한 사항으로 기록합니다.
- **명세에서 새로 구현합니다.** 이 저장소의 명세, 공개 프로토콜 문서, 공개 클라이언트의 블랙박스 관찰만을 근거로 Go로 새로 작성했습니다.

## 핵심 기능

- Claude Code 모델 선택기에 Kiro 모델을 보여 주고, 마지막으로 쓴 모델을 다음 실행에 복원합니다.
- 스트리밍 응답과 도구 호출, 도구 결과를 지원합니다. 이미지 입력은 검증된 형식만 전달합니다.
- 추론 강도(effort)는 가능한 경우에만 맞춥니다. auto 모델이거나 Kiro가 거부하면 기본값으로 진행합니다.
- `--client-history`로 대화를 보관하고 `--resume`으로 이어갈 수 있습니다.
- 사용자의 MCP 서버, 플러그인, 스킬, 커맨드, 에이전트, 출력 스타일, rules를 임시 프로필로 가져옵니다. 일부 제한은 아래에 적었습니다.
- 기존 상태 표시줄 설정이 없으면 Kiro 계정 사용량을 상태 표시줄에 보여 줍니다.
- 턴, 도구 대기, 첫 모델 이벤트에 시간 제한을 둡니다. Ctrl+C로 취소할 수 있고, 종료하면 자신이 띄운 프로세스와 임시 파일을 정리합니다.
- Claude Code의 단발성 웹 검색 요청을 검색 도구만 가진 격리된 Kiro 에이전트로 처리합니다(D146).
- `doctor`, `models`, `install`, `uninstall` 명령으로 점검, 모델 조회, 설치와 제거를 처리합니다.

## 시작하기

### 요구 사항

- macOS Apple silicon
- Kiro CLI 2.x, `kiro-cli login`으로 로그인한 상태 (측정 버전 2.21.3)
- Claude Code 2.x (측정 버전 2.1.269)

같은 메이저 버전의 다른 빌드도 새 측정 없이 실행되며, `doctor`가 unmeasured로 표시합니다. Kiro는 `kiro-cli`와 `kiro-cli-chat`의 버전이 같아야 합니다. 다른 메이저 버전은 거부합니다.

### 릴리스로 설치

```sh
gh release download v0.1.0-dev.1 --repo gnu-gnu/dax-kiro-proxy
shasum -a 256 -c SHA256SUMS
tar -xzf dax-kiro-proxy-v0.1.0-dev.1-darwin-arm64.tar.gz
./dax-kiro-proxy-v0.1.0-dev.1-darwin-arm64/dax-kiro-proxy install
```

브라우저로 받았다면 설치 전에 격리 속성을 지워야 합니다.

```sh
xattr -d com.apple.quarantine dax-kiro-proxy-v0.1.0-dev.1-darwin-arm64/dax-kiro-proxy 2>/dev/null || true
```

### 소스에서 빌드

Go 1.27.1이 필요합니다.

```sh
go build -o ./dist/dax-kiro-proxy ./cmd/dax-kiro-proxy
./dist/dax-kiro-proxy install
```

`install`은 실행 파일과 고지 파일을 `~/.local/bin` 아래 관리 디렉터리에 복사하고 `~/.local/bin/dax-kiro-proxy` 링크를 만듭니다. PATH와 셸 프로필은 바꾸지 않습니다. 아래 명령을 그대로 쓰려면 `~/.local/bin`을 PATH에 추가하세요.

### 실행

```sh
dax-kiro-proxy doctor     # 로그인, 버전, 실행 정책 점검 (모델 호출 없음)
dax-kiro-proxy models     # 사용할 수 있는 Kiro 모델 목록
cd <프로젝트 디렉터리>
dax-kiro-proxy run        # Claude Code 실행
```

`run`은 포그라운드 터미널에서만 실행됩니다. 자주 쓰는 옵션은 다음과 같습니다.

| 옵션 | 설명 |
| --- | --- |
| `--client-history` | 대화를 `~/.claude/projects`에 보관합니다 |
| `--resume <UUID>` | 이전 대화를 이어갑니다. `--client-history`가 함께 적용됩니다 |
| `--model <ID>` | 정확한 Kiro 백엔드 모델 ID를 지정합니다 |
| `--effort <VALUE>` | 시작 추론 강도를 지정합니다 |
| `--turn-timeout`, `--tool-timeout`, `--first-event-timeout` | 턴, 도구 대기, 첫 모델 이벤트의 시간 제한을 바꿉니다 |

전체 옵션은 `dax-kiro-proxy --help`로 확인할 수 있습니다.

## 알려진 제한

- 개인 `~/.claude/CLAUDE.md`는 임시 프로필에서 읽히지 않습니다(D145).
- 임시 rules 경로에만 맞는 `claudeMdExcludes` 패턴은 개인 rule과 그 import를 누락시킬 수 있습니다(D102, D145).
- 원격 플러그인 마켓플레이스, 세션 중 플러그인 변경, 원격 MCP OAuth는 검증하지 않았습니다.
- 실제 Kiro의 이미지 해석은 검증하지 않았습니다.
- 웹 검색의 도메인 제한, 위치 지정, 새 검색 버전, 검색 이어가기는 지원하지 않습니다.
- 같은 대화 UUID를 동시에 이어가면 두 응답이 모두 기록되더라도 다음 요청에는 한쪽만 들어갈 수 있습니다(D105). 병렬 작업은 서로 다른 대화로 하세요.
- `--client-history`나 `--resume`을 쓰면 대화가 `~/.claude/projects`에 남습니다. 마지막 모델 같은 프록시 상태는 `~/.dax-kiro-proxy`에 보관됩니다.

## 문서

- [docs/README.md](docs/README.md): 전체 문서 목록과 필수 읽기 순서
- [docs/USAGE_AND_EVIDENCE.md](docs/USAGE_AND_EVIDENCE.md): 옵션 상세, 호환성과 설치 동작, 기능별 검증 기록
- [docs/PRODUCT_SPEC.md](docs/PRODUCT_SPEC.md): 제품 목표, 범위, 보안 원칙
- [docs/IMPLEMENTATION_DECISIONS.md](docs/IMPLEMENTATION_DECISIONS.md): D번호별 설계 결정
- [AGENTS.md](AGENTS.md): 코딩 에이전트 작업 규칙
