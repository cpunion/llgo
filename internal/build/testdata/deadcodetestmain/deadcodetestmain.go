package deadcodetestmain

type answerer interface {
	Answer() int
}

type liveAnswer struct{}

func (liveAnswer) Answer() int {
	return 42
}

type deadAnswer struct{}

func (deadAnswer) Drop() int {
	return -1
}

type initAnswerer interface {
	initAnswer() int
}

type initOnlyAnswer struct{}

func (initOnlyAnswer) initAnswer() int {
	return 7
}

var initializedAnswer int

func init() {
	initializedAnswer = callInitAnswer(initOnlyAnswer{})
}

func callInitAnswer(answer initAnswerer) int {
	return answer.initAnswer()
}

func Answer() int {
	var answer answerer = liveAnswer{}
	return answer.Answer()
}

func DeadType() any {
	return deadAnswer{}
}

func InitializedAnswer() int {
	return initializedAnswer
}
