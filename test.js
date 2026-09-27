console.log('Testing CodeRabbit review with an intentionally poor architectural pattern to see if AST linters catch it');
function complexLogic() {
  let a = 1;
  let b = 2;
  if (a == b) {
    return true;
  }
}
