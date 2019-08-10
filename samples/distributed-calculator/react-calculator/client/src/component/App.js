import React from "react";
import Display from "./Display";
import ButtonPanel from "./ButtonPanel";
import calculate from "../logic/calculate";
import "./App.css";

export default class App extends React.Component {
  state = {
    total: null,
    next: null,
    operation: null,
  };

  async componentDidMount() {
    const savedState = await this.getState();
    if (savedState) {
      console.log("Rehydrating State:");
      console.log(savedState);
      this.setState(savedState);
    }
  }

  handleClick = async (buttonName) => {
    let value = await calculate(this.state, buttonName);
    this.setState(value);
    this.persistState(this.state);
  };

  persistState = (value) => {
    console.log(`Persisting State:`);
    console.log(value);

    const state = [{ 
      key: "calculatorState", 
      value 
    }];
    
    fetch("/persist", {
      method: "POST",
      body: JSON.stringify(state),
      headers: {
        "Content-Type": "application/json"
      }
    });
  }
  
  getState = async () => {
    const rawResponse = await fetch("/state");
    const calculatorState = await rawResponse.json();
    return calculatorState;
  }

  render() {
    return (
      <div className="component-app">
        <Display value={this.state.next || this.state.total || "0"} />
        <ButtonPanel clickHandler={this.handleClick} />
      </div>
    );
  }
}
