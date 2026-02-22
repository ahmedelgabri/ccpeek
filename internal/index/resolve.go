package index

import (
	"regexp"

	"github.com/ahmedelgabri/ccexplore/internal/model"
)

// todoSessionRe extracts the session UUID from a todo filename like
// "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-ffffffff-gggg-hhhh-iiii-jjjjjjjjjjjj.json"
var todoSessionRe = regexp.MustCompile(
	`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-agent-`,
)

// sessionRef holds the location of a session within the IndexData.Projects slice.
type sessionRef struct {
	projectIdx  int
	sessionIdx  int
	dirName     string
	displayName string
}

// resolveRelationships links related entities in the index by matching
// session IDs, conversation IDs, and project paths.
func resolveRelationships(idx *model.IndexData) {
	// Build sessionID -> sessionRef map
	sessionMap := make(map[string]sessionRef)
	for pi, proj := range idx.Projects {
		for si, sess := range proj.Sessions {
			sessionMap[sess.SessionID] = sessionRef{
				projectIdx:  pi,
				sessionIdx:  si,
				dirName:     proj.DirName,
				displayName: proj.DisplayName,
			}
		}
	}

	// Link todos to sessions
	for i := range idx.Todos {
		m := todoSessionRe.FindStringSubmatch(idx.Todos[i].FileName)
		if m == nil {
			continue
		}
		sessionID := m[1]
		ref, ok := sessionMap[sessionID]
		if !ok {
			continue
		}
		idx.Todos[i].SessionID = sessionID
		idx.Todos[i].ProjectDir = ref.dirName
		idx.Todos[i].ProjectName = ref.displayName

		idx.Projects[ref.projectIdx].Sessions[ref.sessionIdx].TodoFileName = idx.Todos[i].FileName
	}

	// Link file history to sessions
	for i := range idx.FileHistory {
		ref, ok := sessionMap[idx.FileHistory[i].ConversationID]
		if !ok {
			continue
		}
		idx.FileHistory[i].ProjectDir = ref.dirName
		idx.FileHistory[i].ProjectName = ref.displayName

		idx.Projects[ref.projectIdx].Sessions[ref.sessionIdx].HasFileHistory = true
	}
}
