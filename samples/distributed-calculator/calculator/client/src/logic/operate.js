export default async function operate(operandOne, operandTwo, operation) {
  const operationMap = {
  "+": "add",
  "-": "subtract",
  "x": "multiply",
  "÷": "divide"
};
  operandOne = operandOne || "0";
  operandTwo = operandTwo || (operation === "÷" || operation === 'x' ? "1" : "0"); //If dividing or multiplying, then 1 maintains current value in cases of null

  const rawResponse = await fetch(`/calculate/${operationMap[operation]}`, {
    method: 'POST',
    headers: {
      'Accept': 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({operandOne: operandOne, operandTwo: operandTwo}),
  });
  const response = await rawResponse.json();

  return response.toString();
}
